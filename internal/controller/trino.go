/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
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
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

func (r *DataPlatformReconciler) reconcileTrino(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, store objectStore) error {
	ns := dp.Spec.Trino.NamespaceOrDefault()
	dp.Status.TrinoEndpoint = clusterServiceURL(nameTrino, ns, trinoPort)

	catalogData, catalogHash := r.trinoCatalogData(ctx, dp, store)

	coordCfg := trinoConfigProperties(true, dp.Spec.Trino.WorkersOrDefault() == 0, ns, dp.Spec.Trino.ExtraConfig)
	workerCfg := trinoConfigProperties(false, false, ns, dp.Spec.Trino.ExtraConfig)
	cfgHash := hashData(coordCfg, workerCfg, catalogHash)

	if err := r.applyTrinoConfigMap(ctx, dp, ns, coordCfg, workerCfg); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionTrinoReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	if err := r.applyTrinoCatalogSecret(ctx, dp, ns, catalogData); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionTrinoReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	if err := r.applyTrinoService(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionTrinoReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	if err := r.applyTrinoCoordinator(ctx, dp, ns, cfgHash); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionTrinoReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	if err := r.reconcileTrinoWorkers(ctx, dp, ns, cfgHash); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionTrinoReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}

	ready, err := r.deploymentReady(ctx, ns, nameTrino)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionTrinoReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	if !ready {
		setCondition(dp, dataplatformv1alpha1.ConditionTrinoReady, metav1.ConditionFalse, reasonNotReady, "Trino coordinator is not ready")
		return nil
	}
	setCondition(dp, dataplatformv1alpha1.ConditionTrinoReady, metav1.ConditionTrue, reasonReady, "Trino is ready")
	return nil
}

func trinoConfigProperties(coordinator, includeCoordinator bool, ns string, extra map[string]string) string {
	props := map[string]string{
		"coordinator":                        fmt.Sprintf("%t", coordinator),
		"http-server.http.port":              fmt.Sprintf("%d", trinoPort),
		"discovery.uri":                      clusterServiceURL(nameTrino, ns, trinoPort),
		"node-scheduler.include-coordinator": fmt.Sprintf("%t", includeCoordinator),
	}
	maps.Copy(props, extra)
	return renderProperties(props)
}

func renderProperties(props map[string]string) string {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(props[k])
		b.WriteString("\n")
	}
	return b.String()
}

func (r *DataPlatformReconciler) trinoCatalogData(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	store objectStore,
) (map[string][]byte, string) {
	data := map[string][]byte{}
	parts := make([]string, 0, 4)

	if dp.Spec.Lakekeeper.IsEnabled() {
		props := lakekeeperCatalogProperties(dp, store)
		data[nameLakekeeper+".properties"] = []byte(props)
		parts = append(parts, props)
	}
	for name, contents := range dp.Spec.Trino.ExtraCatalogs {
		if name == nameLakekeeper {
			logf.FromContext(ctx).Info("Ignoring extraCatalogs entry; lakekeeper catalog is operator-owned", "name", name)
			continue
		}
		data[name+".properties"] = []byte(contents)
		parts = append(parts, name, contents)
	}
	return data, hashData(parts...)
}

func lakekeeperCatalogProperties(dp *dataplatformv1alpha1.DataPlatform, store objectStore) string {
	lkNS := dp.Spec.Lakekeeper.NamespaceOrDefault()
	uri := clusterServiceURL(nameLakekeeper, lkNS, lakekeeperPort) + "/catalog"
	props := map[string]string{
		"connector.name":                                  "iceberg",
		"iceberg.catalog.type":                            "rest",
		"iceberg.rest-catalog.uri":                        uri,
		"iceberg.rest-catalog.warehouse":                  dp.Spec.Lakekeeper.Warehouse.NameOrDefault(),
		"iceberg.rest-catalog.nested-namespace-enabled":   strconv.FormatBool(true),
		"iceberg.unique-table-location":                   strconv.FormatBool(true),
		"fs.native-s3.enabled":                            strconv.FormatBool(true),
		"s3.region":                                       store.Region,
		"iceberg.rest-catalog.vended-credentials-enabled": strconv.FormatBool(store.STSEnabled),
	}
	if !store.STSEnabled {
		props["s3.aws-access-key"] = store.AccessKeyID
		props["s3.aws-secret-key"] = store.SecretAccessKey
	}
	if store.Endpoint != "" {
		props["s3.endpoint"] = store.Endpoint
	}
	if store.PathStyleAccess {
		props["s3.path-style-access"] = strconv.FormatBool(true)
	}
	return renderProperties(props)
}

func (r *DataPlatformReconciler) applyTrinoConfigMap(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns, coordCfg, workerCfg string,
) error {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: configMapTrino, Namespace: ns}}
	labels := labelsFor(dp, componentTrinoCoordinator)
	return r.apply(ctx, dp, cm, func() error {
		ensureLabels(cm, labels)
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data["config.properties.coordinator"] = coordCfg
		cm.Data["config.properties.worker"] = workerCfg
		return nil
	})
}

func (r *DataPlatformReconciler) applyTrinoCatalogSecret(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
	data map[string][]byte,
) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretTrinoCatalog, Namespace: ns}}
	labels := labelsFor(dp, componentTrinoCoordinator)
	return r.apply(ctx, dp, secret, func() error {
		ensureLabels(secret, labels)
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = data
		return nil
	})
}

func (r *DataPlatformReconciler) applyTrinoService(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: nameTrino, Namespace: ns}}
	labels := labelsFor(dp, componentTrinoCoordinator)
	return r.apply(ctx, dp, svc, func() error {
		ensureLabels(svc, labels)
		svc.Spec.Type = dp.Spec.Trino.Service.TypeOrDefault()
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       portNameHTTP,
			Port:       trinoPort,
			TargetPort: intstr.FromInt32(trinoPort),
		}}
		return nil
	})
}

func (r *DataPlatformReconciler) applyTrinoCoordinator(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns, cfgHash string,
) error {
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: nameTrino, Namespace: ns}}
	labels := labelsFor(dp, componentTrinoCoordinator)
	return r.apply(ctx, dp, deploy, func() error {
		ensureLabels(deploy, labels)
		if deploy.CreationTimestamp.IsZero() {
			deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		}
		deploy.Spec.Replicas = ptr.To(int32(1))
		if deploy.Spec.Template.Annotations == nil {
			deploy.Spec.Template.Annotations = map[string]string{}
		}
		deploy.Spec.Template.Annotations[annotationConfigHash] = cfgHash
		deploy.Spec.Template.Labels = labels
		deploy.Spec.Template.Spec = trinoPodSpec(
			dp.Spec.Trino.ImageOrDefault(),
			"config.properties.coordinator",
			dp.Spec.Trino.Coordinator.Resources,
			dp.Spec.Trino.ExtraEnv,
		)
		return nil
	})
}

func (r *DataPlatformReconciler) reconcileTrinoWorkers(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns, cfgHash string,
) error {
	workers := dp.Spec.Trino.WorkersOrDefault()
	if workers == 0 {
		deploy := &appsv1.Deployment{}
		err := r.Get(ctx, types.NamespacedName{Name: nameTrinoWorker, Namespace: ns}, deploy)
		if errors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		logf.FromContext(ctx).Info("Deleting Trino worker Deployment", "name", nameTrinoWorker)
		return client.IgnoreNotFound(r.Delete(ctx, deploy))
	}

	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: nameTrinoWorker, Namespace: ns}}
	labels := labelsFor(dp, componentTrinoWorker)
	return r.apply(ctx, dp, deploy, func() error {
		ensureLabels(deploy, labels)
		if deploy.CreationTimestamp.IsZero() {
			deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		}
		deploy.Spec.Replicas = ptr.To(workers)
		if deploy.Spec.Template.Annotations == nil {
			deploy.Spec.Template.Annotations = map[string]string{}
		}
		deploy.Spec.Template.Annotations[annotationConfigHash] = cfgHash
		deploy.Spec.Template.Labels = labels
		deploy.Spec.Template.Spec = trinoPodSpec(
			dp.Spec.Trino.ImageOrDefault(),
			"config.properties.worker",
			dp.Spec.Trino.Resources,
			dp.Spec.Trino.ExtraEnv,
		)
		return nil
	})
}

func trinoPodSpec(image, configKey string, resources corev1.ResourceRequirements, env []corev1.EnvVar) corev1.PodSpec {
	return corev1.PodSpec{
		SecurityContext: restrictedPodSecurity(),
		Containers: []corev1.Container{{
			Name:  nameTrino,
			Image: image,
			Ports: []corev1.ContainerPort{{Name: portNameHTTP, ContainerPort: trinoPort}},
			Env:   env,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "config", MountPath: "/etc/trino/config.properties", SubPath: configKey},
				{Name: "catalog", MountPath: "/etc/trino/catalog"},
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/v1/info",
						Port: intstr.FromInt32(trinoPort),
					},
				},
				PeriodSeconds: 10,
			},
			Resources:       resources,
			SecurityContext: restrictedContainerSecurity(),
		}},
		Volumes: []corev1.Volume{
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: configMapTrino},
					},
				},
			},
			{
				Name: "catalog",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: secretTrinoCatalog},
				},
			},
		},
	}
}
