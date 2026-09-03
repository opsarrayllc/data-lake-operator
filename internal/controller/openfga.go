/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

    10|Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

type openfgaConfig struct {
	enabled  bool
	endpoint string
	store    string
	apiKey   string
	opaURL   string
	// httpEndpoint is the REST API, which OPA uses to resolve row filters.
	// endpoint is the gRPC one LakeKeeper speaks.
	httpEndpoint string
	// rowFilterStoreID is empty until OpenFGA is up and the store has been
	// provisioned. Row filters and column masks fail closed until it is known.
	rowFilterStoreID string
}

func (r *DataPlatformReconciler) reconcileAuthz(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform) (openfgaConfig, bool, error) {
	if !dp.Spec.Authz.IsEnabled() {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionTrue, reasonDisabled, "Authorization is disabled")
		return openfgaConfig{}, true, nil
	}
	if !dp.Spec.Authz.IsEmbedded() {
		err := fmt.Errorf("external OpenFGA is not supported yet; set spec.authz.embedded=true")
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonMissing, err.Error())
		return openfgaConfig{}, false, err
	}
	return r.reconcileOpenFGA(ctx, dp)
}

func (r *DataPlatformReconciler) reconcileOpenFGA(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform) (openfgaConfig, bool, error) {
	spec := dp.Spec.Authz.OpenFGA
	ns := spec.NamespaceOrDefault()
	dp.Status.OpenFGAEndpoint = clusterServiceURL(nameOpenFGA, ns, openfgaGRPCPort)

	if err := r.ensureNamespace(ctx, dp, ns, componentOpenFGA); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonError, err.Error())
		return openfgaConfig{}, false, err
	}
	if err := r.ensureOpenFGASecrets(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonError, err.Error())
		return openfgaConfig{}, false, err
	}
	if err := r.applyOpenFGAPostgres(ctx, dp, ns, spec.Postgres); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonError, err.Error())
		return openfgaConfig{}, false, err
	}
	if err := r.applyOpenFGAService(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonError, err.Error())
		return openfgaConfig{}, false, err
	}

	pgReady, err := r.statefulSetReady(ctx, ns, namePostgres)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonError, err.Error())
		return openfgaConfig{}, false, err
	}
	if !pgReady {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonNotReady, "OpenFGA Postgres is not ready")
		return openfgaConfig{enabled: true, endpoint: dp.Status.OpenFGAEndpoint, store: spec.StoreOrDefault()}, false, nil
	}

	if err := r.applyOpenFGADeployment(ctx, dp, ns, spec); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonError, err.Error())
		return openfgaConfig{}, false, err
	}

	apiKey, err := r.getSecretData(ctx, secretOpenFGA, ns, keyOpenFGAAPIKey)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonError, err.Error())
		return openfgaConfig{}, false, err
	}
	cfg := openfgaConfig{
		enabled:      true,
		endpoint:     dp.Status.OpenFGAEndpoint,
		store:        spec.StoreOrDefault(),
		apiKey:       apiKey,
		opaURL:       clusterServiceURL(nameOPA, ns, opaPort),
		httpEndpoint: clusterServiceURL(nameOpenFGA, ns, openfgaHTTPPort),
	}

	ready, err := r.deploymentReady(ctx, ns, nameOpenFGA)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonError, err.Error())
		return cfg, false, err
	}
	if !ready {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonNotReady, "OpenFGA Deployment is not ready")
		return cfg, false, nil
	}

	storeID, err := r.reconcileAccessStore(ctx, dp, ns, apiKey)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionFalse, reasonError, err.Error())
		return cfg, false, err
	}
	cfg.rowFilterStoreID = storeID

	setCondition(dp, dataplatformv1alpha1.ConditionOpenFGAReady, metav1.ConditionTrue, reasonReady, "OpenFGA is ready")
	return cfg, true, nil
}

// reconcileAccessStore provisions the OpenFGA store and authorization model
// backing spec.authz.rowFilters and spec.authz.columnAccess, and returns its
// store id.
func (r *DataPlatformReconciler) reconcileAccessStore(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns, apiKey string,
) (string, error) {
	log := logf.FromContext(ctx)
	if !dp.Spec.Authz.HasAccessPolicies() || !dp.Spec.Trino.IsEnabled() {
		dp.Status.RowFilterStoreID = ""
		return "", nil
	}
	if r.Catalog == nil {
		return "", fmt.Errorf("catalog client is not configured")
	}
	req := AuthzStoreRequest{
		Store: dp.Spec.Authz.OpenFGA.RowFilterStoreOrDefault(),
		Types: accessAuthzTypes(dp.Spec.Authz),
	}
	storeID, err := r.Catalog.EnsureAuthzStore(ctx, ns, nameOpenFGA, openfgaHTTPPort, apiKey, req)
	if err != nil {
		return "", err
	}
	if storeID != dp.Status.RowFilterStoreID {
		log.Info("Ensured OpenFGA access store", "store", req.Store, "id", storeID)
	}
	dp.Status.RowFilterStoreID = storeID
	return storeID, nil
}

// accessAuthzTypes collapses row filters and column restrictions into the
// OpenFGA object types they need, merging relations that share a type.
func accessAuthzTypes(spec dataplatformv1alpha1.AuthzSpec) []AuthzType {
	relations := map[string][]string{}
	add := func(name, relation string) {
		if !slices.Contains(relations[name], relation) {
			relations[name] = append(relations[name], relation)
		}
	}
	for i := range spec.RowFilters {
		add(spec.RowFilters[i].OpenFGA.Type, spec.RowFilters[i].OpenFGA.RelationOrDefault())
	}
	for i := range spec.ColumnAccess {
		add(spec.ColumnAccess[i].OpenFGA.Type, spec.ColumnAccess[i].OpenFGA.RelationOrDefault())
	}
	types := make([]AuthzType, 0, len(relations))
	for _, name := range slices.Sorted(maps.Keys(relations)) {
		types = append(types, AuthzType{Name: name, Relations: slices.Sorted(slices.Values(relations[name]))})
	}
	return types
}

func (r *DataPlatformReconciler) ensureOpenFGASecrets(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, ns string) error {
	apiKey, err := randomHex(32)
	if err != nil {
		return err
	}
	if err := r.ensureGeneratedSecret(ctx, dp, secretOpenFGA, ns, componentOpenFGA, map[string][]byte{
		keyOpenFGAAPIKey: []byte(apiKey),
	}); err != nil {
		return err
	}
	password, err := randomHex(16)
	if err != nil {
		return err
	}
	return r.ensureGeneratedSecret(ctx, dp, secretPostgres, ns, componentOpenFGAPostgres, map[string][]byte{
		keyPostgresUsername: []byte(namePostgres),
		keyPostgresPassword: []byte(password),
		keyPostgresDatabase: []byte("postgres"),
	})
}

func (r *DataPlatformReconciler) applyOpenFGAPostgres(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
	spec dataplatformv1alpha1.PostgresSpec,
) error {
	qty, err := resource.ParseQuantity(spec.StorageSizeOrDefault())
	if err != nil {
		return fmt.Errorf("invalid openfga postgres storageSize: %w", err)
	}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: namePostgres, Namespace: ns}}
	labels := labelsFor(dp, componentOpenFGAPostgres)
	if err := r.apply(ctx, dp, svc, func() error {
		ensureLabels(svc, labels)
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       namePostgres,
			Port:       postgresPort,
			TargetPort: intstr.FromInt32(postgresPort),
		}}
		return nil
	}); err != nil {
		return err
	}

	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: namePostgres, Namespace: ns}}
	return r.apply(ctx, dp, sts, func() error {
		ensureLabels(sts, labels)
		if sts.CreationTimestamp.IsZero() {
			sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
			sts.Spec.ServiceName = namePostgres
			sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: volumeData},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: qty},
					},
				},
			}}
		}
		sts.Spec.Replicas = ptr.To(int32(1))
		sts.Spec.Template.Labels = labels
		sts.Spec.Template.Spec.SecurityContext = restrictedPodSecurity(uidPostgres, gidPostgres)
		sts.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  namePostgres,
			Image: spec.ImageOrDefault(),
			Ports: []corev1.ContainerPort{{Name: namePostgres, ContainerPort: postgresPort}},
			Env: []corev1.EnvVar{
				{Name: "POSTGRES_USER", Value: namePostgres},
				{
					Name: "POSTGRES_PASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: secretPostgres},
							Key:                  keyPostgresPassword,
						},
					},
				},
				{Name: "POSTGRES_DB", Value: "postgres"},
				{Name: keyPGDATA, Value: pgDataPath},
			},
			VolumeMounts: []corev1.VolumeMount{{Name: volumeData, MountPath: pgDataMountPath}},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(postgresPort)},
				},
				PeriodSeconds: 5,
			},
			SecurityContext: restrictedContainerSecurity(uidPostgres, gidPostgres),
		}}
		return nil
	})
}

func (r *DataPlatformReconciler) applyOpenFGAService(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, ns string) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: nameOpenFGA, Namespace: ns}}
	labels := labelsFor(dp, componentOpenFGA)
	return r.apply(ctx, dp, svc, func() error {
		ensureLabels(svc, labels)
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{
			{Name: "grpc", Port: openfgaGRPCPort, TargetPort: intstr.FromInt32(openfgaGRPCPort)},
			{Name: portNameHTTP, Port: openfgaHTTPPort, TargetPort: intstr.FromInt32(openfgaHTTPPort)},
		}
		return nil
	})
}

func (r *DataPlatformReconciler) applyOpenFGADeployment(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
	spec dataplatformv1alpha1.OpenFGASpec,
) error {
	password, err := r.getSecretData(ctx, secretPostgres, ns, keyPostgresPassword)
	if err != nil {
		return err
	}
	uri := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/postgres?sslmode=disable",
		namePostgres, url.QueryEscape(password), namePostgres, postgresPort,
	)
	env := []corev1.EnvVar{
		{Name: "OPENFGA_DATASTORE_ENGINE", Value: "postgres"},
		{Name: "OPENFGA_DATASTORE_URI", Value: uri},
		{Name: "OPENFGA_PLAYGROUND_ENABLED", Value: "false"},
		{Name: "OPENFGA_AUTHN_METHOD", Value: "preshared"},
		{
			Name: "OPENFGA_AUTHN_PRESHARED_KEYS",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretOpenFGA},
					Key:                  keyOpenFGAAPIKey,
				},
			},
		},
	}

	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: nameOpenFGA, Namespace: ns}}
	labels := labelsFor(dp, componentOpenFGA)
	return r.apply(ctx, dp, deploy, func() error {
		ensureLabels(deploy, labels)
		if deploy.CreationTimestamp.IsZero() {
			deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		}
		deploy.Spec.Replicas = ptr.To(int32(1))
		deploy.Spec.Template.Labels = labels
		deploy.Spec.Template.Spec.SecurityContext = restrictedPodSecurity(uidOpenFGA, gidOpenFGA)
		deploy.Spec.Template.Spec.InitContainers = []corev1.Container{{
			Name:            "migrate",
			Image:           spec.ImageOrDefault(),
			Args:            []string{"migrate"},
			Env:             env,
			SecurityContext: restrictedContainerSecurity(uidOpenFGA, gidOpenFGA),
		}}
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  nameOpenFGA,
			Image: spec.ImageOrDefault(),
			Args:  []string{"run"},
			Env:   env,
			Ports: []corev1.ContainerPort{
				{Name: "grpc", ContainerPort: openfgaGRPCPort},
				{Name: portNameHTTP, ContainerPort: openfgaHTTPPort},
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(openfgaGRPCPort)},
				},
				PeriodSeconds: 5,
			},
			Resources:       spec.Resources,
			SecurityContext: restrictedContainerSecurity(uidOpenFGA, gidOpenFGA),
		}}
		return nil
	})
}
