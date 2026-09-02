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
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

func (r *DataPlatformReconciler) reconcileLakekeeper(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	conn postgresConn,
	oidc oidcConfig,
) error {
	ns := dp.Spec.Lakekeeper.NamespaceOrDefault()
	dp.Status.LakekeeperEndpoint = clusterServiceURL(nameLakekeeper, ns, lakekeeperPort)

	if err := r.ensureEncryptionSecret(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionLakekeeperReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	if err := r.applyLakekeeperService(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionLakekeeperReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	if err := r.applyLakekeeperDeployment(ctx, dp, ns, conn, oidc); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionLakekeeperReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}

	ready, err := r.deploymentReady(ctx, ns, nameLakekeeper)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionLakekeeperReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	if !ready {
		setCondition(dp, dataplatformv1alpha1.ConditionLakekeeperReady, metav1.ConditionFalse, reasonNotReady, "LakeKeeper Deployment is not ready")
		return nil
	}
	setCondition(dp, dataplatformv1alpha1.ConditionLakekeeperReady, metav1.ConditionTrue, reasonReady, "LakeKeeper is ready")
	return nil
}

func (r *DataPlatformReconciler) ensureEncryptionSecret(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
) error {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretEncryption, Namespace: ns}, secret)
	if err == nil {
		return nil
	}
	key, err := randomHex(32)
	if err != nil {
		return err
	}
	return r.ensureGeneratedSecret(ctx, dp, secretEncryption, ns, componentLakekeeper, map[string][]byte{
		keyEncryption: []byte(key),
	})
}

func (r *DataPlatformReconciler) applyLakekeeperService(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: nameLakekeeper, Namespace: ns}}
	labels := labelsFor(dp, componentLakekeeper)
	return r.apply(ctx, dp, svc, func() error {
		ensureLabels(svc, labels)
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       portNameHTTP,
			Port:       lakekeeperPort,
			TargetPort: intstr.FromInt32(lakekeeperPort),
		}}
		return nil
	})
}

func (r *DataPlatformReconciler) applyLakekeeperDeployment(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
	conn postgresConn,
	oidc oidcConfig,
) error {
	spec := dp.Spec.Lakekeeper
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: nameLakekeeper, Namespace: ns}}
	labels := labelsFor(dp, componentLakekeeper)
	env := lakekeeperEnv(dp, conn, oidc)
	env = append(env, spec.ExtraEnv...)

	pgWait := fmt.Sprintf(
		"until pg_isready -h %s -p %d -U %s; do sleep 2; done",
		conn.Host, conn.Port, conn.Username,
	)

	return r.apply(ctx, dp, deploy, func() error {
		ensureLabels(deploy, labels)
		if deploy.CreationTimestamp.IsZero() {
			deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		}
		deploy.Spec.Replicas = ptr.To(spec.ReplicasOrDefault())
		deploy.Spec.Template.Labels = labels
		deploy.Spec.Template.Spec.SecurityContext = restrictedPodSecurity(0, 0)
		deploy.Spec.Template.Spec.InitContainers = []corev1.Container{
			{
				Name:            "wait-for-postgres",
				Image:           spec.Postgres.ImageOrDefault(),
				Command:         []string{"sh", "-c", pgWait},
				SecurityContext: restrictedContainerSecurity(uidPostgres, gidPostgres),
			},
			{
				Name:            "migrate",
				Image:           spec.ImageOrDefault(),
				Args:            []string{"migrate"},
				SecurityContext: restrictedContainerSecurity(uidLakekeeper, gidLakekeeper),
				Env:             env,
			},
		}
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  nameLakekeeper,
			Image: spec.ImageOrDefault(),
			Args:  []string{"serve"},
			Env:   env,
			Ports: []corev1.ContainerPort{{Name: portNameHTTP, ContainerPort: lakekeeperPort}},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(lakekeeperPort)},
				},
				PeriodSeconds: 5,
			},
			Resources:       spec.Resources,
			SecurityContext: restrictedContainerSecurity(uidLakekeeper, gidLakekeeper),
		}}
		return nil
	})
}

func lakekeeperEnv(dp *dataplatformv1alpha1.DataPlatform, conn postgresConn, oidc oidcConfig) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{
			Name: keyEncryption,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretEncryption},
					Key:                  keyEncryption,
				},
			},
		},
		{Name: "LAKEKEEPER__PG_HOST_W", Value: conn.Host},
		{Name: "LAKEKEEPER__PG_PORT", Value: strconv.Itoa(int(conn.Port))},
		{Name: "LAKEKEEPER__PG_DATABASE", Value: conn.Database},
		{Name: "LAKEKEEPER__BASE_URI", Value: dp.Status.LakekeeperEndpoint},
	}
	if conn.SSLMode != "" {
		env = append(env, corev1.EnvVar{Name: "LAKEKEEPER__PG_SSL_MODE", Value: conn.SSLMode})
	}
	if oidc.enabled {
		env = append(env,
			corev1.EnvVar{Name: "LAKEKEEPER__OPENID_PROVIDER_URI", Value: oidc.issuer},
			corev1.EnvVar{Name: "LAKEKEEPER__OPENID_AUDIENCE", Value: oidc.audience},
			corev1.EnvVar{Name: "LAKEKEEPER__UI__OPENID_CLIENT_ID", Value: oidc.uiClientID},
			corev1.EnvVar{Name: "LAKEKEEPER__UI__OPENID_SCOPE", Value: oidc.scope},
			corev1.EnvVar{Name: "LAKEKEEPER__AUTHZ_BACKEND", Value: "allowall"},
		)
		if oidc.publicIssuer != "" && oidc.publicIssuer != oidc.issuer {
			env = append(env,
				corev1.EnvVar{Name: "LAKEKEEPER__OPENID_ADDITIONAL_ISSUERS", Value: oidc.publicIssuer},
				corev1.EnvVar{Name: "LAKEKEEPER__UI__OPENID_PROVIDER_URI", Value: oidc.publicIssuer},
			)
		}
	}
	if dp.Spec.Lakekeeper.Postgres.IsEmbedded() {
		env = append(env,
			corev1.EnvVar{
				Name: "LAKEKEEPER__PG_USER",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: secretPostgres},
						Key:                  keyPostgresUsername,
					},
				},
			},
			corev1.EnvVar{
				Name: "LAKEKEEPER__PG_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: secretPostgres},
						Key:                  keyPostgresPassword,
					},
				},
			},
		)
		return env
	}
	return append(env,
		corev1.EnvVar{Name: "LAKEKEEPER__PG_USER", Value: conn.Username},
		corev1.EnvVar{Name: "LAKEKEEPER__PG_PASSWORD", Value: conn.Password},
	)
}

func (r *DataPlatformReconciler) deploymentReady(ctx context.Context, ns, name string) (bool, error) {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, deploy); err != nil {
		return false, err
	}
	return deploy.Status.ReadyReplicas >= 1, nil
}
