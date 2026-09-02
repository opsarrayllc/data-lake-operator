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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

type postgresConn struct {
	Host     string
	Port     int32
	Database string
	Username string
	Password string
	SSLMode  string
}

func (r *DataPlatformReconciler) reconcilePostgres(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
) (postgresConn, error) {
	ns := dp.Spec.Lakekeeper.NamespaceOrDefault()
	spec := dp.Spec.Lakekeeper.Postgres

	if !spec.IsEmbedded() {
		return r.externalPostgres(ctx, dp, spec)
	}

	if err := r.ensurePostgresSecret(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionFalse, reasonError, err.Error())
		return postgresConn{}, err
	}

	if err := r.applyPostgresService(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionFalse, reasonError, err.Error())
		return postgresConn{}, err
	}

	if err := r.applyPostgresStatefulSet(ctx, dp, ns, spec); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionFalse, reasonError, err.Error())
		return postgresConn{}, err
	}

	ready, err := r.statefulSetReady(ctx, ns, namePostgres)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionFalse, reasonError, err.Error())
		return postgresConn{}, err
	}
	conn := postgresConn{
		Host:     namePostgres,
		Port:     postgresPort,
		Database: spec.DatabaseOrDefault(),
		Username: namePostgres,
		SSLMode:  sslModeDisable,
	}
	if !ready {
		setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionFalse, reasonNotReady, "Postgres StatefulSet is not ready")
		return conn, nil
	}
	setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionTrue, reasonReady, "Postgres is ready")
	return conn, nil
}

func (r *DataPlatformReconciler) externalPostgres(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	spec dataplatformv1alpha1.PostgresSpec,
) (postgresConn, error) {
	if spec.Host == "" {
		err := fmt.Errorf("lakekeeper.postgres.host is required when embedded is false")
		setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionFalse, reasonMissing, err.Error())
		return postgresConn{}, err
	}
	if spec.CredentialsSecretRef == nil || spec.CredentialsSecretRef.Name == "" {
		err := fmt.Errorf("lakekeeper.postgres.credentialsSecretRef is required when embedded is false")
		setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionFalse, reasonMissing, err.Error())
		return postgresConn{}, err
	}
	ref := spec.CredentialsSecretRef
	secretNS := ref.Namespace
	if secretNS == "" {
		secretNS = dp.Spec.Lakekeeper.NamespaceOrDefault()
	}
	user, err := r.getSecretData(ctx, ref.Name, secretNS, ref.UsernameKeyOrDefault())
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionFalse, reasonError, err.Error())
		return postgresConn{}, err
	}
	pass, err := r.getSecretData(ctx, ref.Name, secretNS, ref.PasswordKeyOrDefault())
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionFalse, reasonError, err.Error())
		return postgresConn{}, err
	}
	setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionTrue, reasonReady, "Using external Postgres")
	return postgresConn{
		Host:     spec.Host,
		Port:     spec.PortOrDefault(),
		Database: spec.DatabaseOrDefault(),
		Username: user,
		Password: pass,
		SSLMode:  spec.SSLMode,
	}, nil
}

func (r *DataPlatformReconciler) ensurePostgresSecret(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
) error {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretPostgres, Namespace: ns}, secret)
	if err == nil {
		return nil
	}
	password, err := randomHex(16)
	if err != nil {
		return err
	}
	db := dp.Spec.Lakekeeper.Postgres.DatabaseOrDefault()
	return r.ensureGeneratedSecret(ctx, dp, secretPostgres, ns, componentPostgres, map[string][]byte{
		keyPostgresUsername: []byte(namePostgres),
		keyPostgresPassword: []byte(password),
		keyPostgresDatabase: []byte(db),
	})
}

func (r *DataPlatformReconciler) applyPostgresService(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: namePostgres, Namespace: ns}}
	labels := labelsFor(dp, componentPostgres)
	return r.apply(ctx, dp, svc, func() error {
		ensureLabels(svc, labels)
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       namePostgres,
			Port:       postgresPort,
			TargetPort: intstr.FromInt32(postgresPort),
		}}
		return nil
	})
}

func (r *DataPlatformReconciler) applyPostgresStatefulSet(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
	spec dataplatformv1alpha1.PostgresSpec,
) error {
	qty, err := resource.ParseQuantity(spec.StorageSizeOrDefault())
	if err != nil {
		return fmt.Errorf("invalid postgres storageSize: %w", err)
	}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: namePostgres, Namespace: ns}}
	labels := labelsFor(dp, componentPostgres)
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
				{Name: "POSTGRES_DB", Value: spec.DatabaseOrDefault()},
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

func (r *DataPlatformReconciler) statefulSetReady(ctx context.Context, ns, name string) (bool, error) {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, sts); err != nil {
		return false, err
	}
	return sts.Status.ReadyReplicas >= 1, nil
}
