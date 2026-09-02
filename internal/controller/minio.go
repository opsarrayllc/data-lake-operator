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
	"net/url"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

type objectStore struct {
	Bucket          string
	Region          string
	Endpoint        string
	PathStyleAccess bool
	Flavor          string
	STSEnabled      bool
	STSRoleARN      string
	AccessKeyID     string
	SecretAccessKey string
}

func (r *DataPlatformReconciler) reconcileObjectStore(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
) (objectStore, bool, error) {
	if !dp.Spec.Storage.IsEmbedded() {
		store, err := r.externalObjectStore(ctx, dp)
		if err != nil {
			return objectStore{}, false, err
		}
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionTrue, reasonDisabled, "Using external object store")
		return store, true, nil
	}

	store, ready, err := r.reconcileMinio(ctx, dp)
	if err != nil {
		return objectStore{}, false, err
	}
	return store, ready, nil
}

func (r *DataPlatformReconciler) externalObjectStore(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
) (objectStore, error) {
	s3 := dp.Spec.Storage.S3
	if s3 == nil {
		err := fmt.Errorf("spec.storage.s3 is required when storage.embedded is false")
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonMissing, err.Error())
		return objectStore{}, err
	}
	if s3.CredentialsSecretRef == nil || s3.CredentialsSecretRef.Name == "" {
		err := fmt.Errorf("spec.storage.s3.credentialsSecretRef is required when storage.embedded is false")
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonMissing, err.Error())
		return objectStore{}, err
	}
	ref := s3.CredentialsSecretRef
	accessKey, err := r.getSecretData(ctx, ref.Name, ref.Namespace, ref.AccessKeyIDKeyOrDefault())
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonError, err.Error())
		return objectStore{}, err
	}
	secretKey, err := r.getSecretData(ctx, ref.Name, ref.Namespace, ref.SecretAccessKeyKeyOrDefault())
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonError, err.Error())
		return objectStore{}, err
	}
	return objectStore{
		Bucket:          s3.Bucket,
		Region:          s3.Region,
		Endpoint:        s3.Endpoint,
		PathStyleAccess: s3.PathStyleAccess,
		Flavor:          s3.FlavorOrDefault(),
		STSEnabled:      s3.STSEnabled,
		STSRoleARN:      s3.STSRoleARN,
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
	}, nil
}

func (r *DataPlatformReconciler) reconcileMinio(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
) (objectStore, bool, error) {
	spec := dp.Spec.Storage.Minio
	ns := spec.NamespaceOrDefault()
	dp.Status.MinioEndpoint = clusterServiceURL(nameMinio, ns, minioAPIPort)

	if err := r.ensureNamespace(ctx, dp, ns, componentMinio); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonError, err.Error())
		return objectStore{}, false, err
	}
	if err := r.ensureMinioSecret(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonError, err.Error())
		return objectStore{}, false, err
	}
	if err := r.applyMinioService(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonError, err.Error())
		return objectStore{}, false, err
	}
	if err := r.applyMinioStatefulSet(ctx, dp, ns, spec); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonError, err.Error())
		return objectStore{}, false, err
	}

	accessKey, err := r.getSecretData(ctx, secretMinio, ns, keyS3Access)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonError, err.Error())
		return objectStore{}, false, err
	}
	secretKey, err := r.getSecretData(ctx, secretMinio, ns, keyS3Secret)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonError, err.Error())
		return objectStore{}, false, err
	}

	store := objectStore{
		Bucket:          spec.BucketOrDefault(),
		Region:          dataplatformv1alpha1.DefaultS3CompatRegion,
		Endpoint:        dp.Status.MinioEndpoint,
		PathStyleAccess: true,
		Flavor:          dataplatformv1alpha1.DefaultS3CompatFlavor,
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
	}

	ready, err := r.statefulSetReady(ctx, ns, nameMinio)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonError, err.Error())
		return store, false, err
	}
	if !ready {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonNotReady, "MinIO StatefulSet is not ready")
		return store, false, nil
	}

	bucketReady, err := r.ensureMinioBucket(ctx, dp, ns, spec, accessKey, secretKey)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonError, err.Error())
		return store, false, err
	}
	if !bucketReady {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionFalse, reasonNotReady, "Waiting for MinIO bucket to be created")
		return store, false, nil
	}

	setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionTrue, reasonReady, "MinIO is ready")
	return store, true, nil
}

func (r *DataPlatformReconciler) ensureMinioSecret(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
) error {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretMinio, Namespace: ns}, secret)
	if err == nil {
		return nil
	}
	access, err := randomHex(8)
	if err != nil {
		return err
	}
	secretKey, err := randomHex(16)
	if err != nil {
		return err
	}
	return r.ensureGeneratedSecret(ctx, dp, secretMinio, ns, componentMinio, map[string][]byte{
		keyS3Access: []byte(access),
		keyS3Secret: []byte(secretKey),
	})
}

func (r *DataPlatformReconciler) applyMinioService(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: nameMinio, Namespace: ns}}
	labels := labelsFor(dp, componentMinio)
	return r.apply(ctx, dp, svc, func() error {
		ensureLabels(svc, labels)
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{
			{Name: "api", Port: minioAPIPort, TargetPort: intstr.FromInt32(minioAPIPort)},
			{Name: "console", Port: minioConsolePort, TargetPort: intstr.FromInt32(minioConsolePort)},
		}
		return nil
	})
}

func (r *DataPlatformReconciler) applyMinioStatefulSet(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
	spec dataplatformv1alpha1.MinioSpec,
) error {
	qty, err := resource.ParseQuantity(spec.StorageSizeOrDefault())
	if err != nil {
		return fmt.Errorf("invalid minio storageSize: %w", err)
	}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: nameMinio, Namespace: ns}}
	labels := labelsFor(dp, componentMinio)
	return r.apply(ctx, dp, sts, func() error {
		ensureLabels(sts, labels)
		if sts.CreationTimestamp.IsZero() {
			sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
			sts.Spec.ServiceName = nameMinio
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
		sts.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot:   ptr.To(true),
			FSGroup:        ptr.To(int64(1000)),
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		}
		sts.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  nameMinio,
			Image: spec.ImageOrDefault(),
			Args:  []string{"server", "/data", "--console-address", fmt.Sprintf(":%d", minioConsolePort)},
			Ports: []corev1.ContainerPort{
				{Name: "api", ContainerPort: minioAPIPort},
				{Name: "console", ContainerPort: minioConsolePort},
			},
			Env: []corev1.EnvVar{
				{
					Name: "MINIO_ROOT_USER",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: secretMinio},
							Key:                  keyS3Access,
						},
					},
				},
				{
					Name: "MINIO_ROOT_PASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: secretMinio},
							Key:                  keyS3Secret,
						},
					},
				},
			},
			VolumeMounts: []corev1.VolumeMount{{Name: volumeData, MountPath: "/data"}},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/minio/health/ready",
						Port: intstr.FromInt32(minioAPIPort),
					},
				},
				PeriodSeconds: 5,
			},
			Resources:       spec.Resources,
			SecurityContext: restrictedContainerSecurity(),
		}}
		return nil
	})
}

func (r *DataPlatformReconciler) ensureMinioBucket(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
	spec dataplatformv1alpha1.MinioSpec,
	accessKey, secretKey string,
) (bool, error) {
	mcHost := fmt.Sprintf("http://%s:%s@%s.%s.svc:%d",
		url.PathEscape(accessKey), url.PathEscape(secretKey), nameMinio, ns, minioAPIPort)
	mcSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretMinioMC, Namespace: ns}}
	if err := r.apply(ctx, dp, mcSecret, func() error {
		ensureLabels(mcSecret, labelsFor(dp, componentMinio))
		mcSecret.Type = corev1.SecretTypeOpaque
		mcSecret.Data = map[string][]byte{keyMCHost: []byte(mcHost)}
		return nil
	}); err != nil {
		return false, err
	}

	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: nameMinioBucketJob, Namespace: ns}, job)
	if err == nil {
		if !job.DeletionTimestamp.IsZero() {
			return false, nil
		}
		if jobSucceeded(job) {
			return true, nil
		}
		if jobFailed(job) {
			if delErr := r.Delete(ctx, job); delErr != nil {
				return false, delErr
			}
			return false, nil
		}
		return false, nil
	}
	if !errors.IsNotFound(err) {
		return false, err
	}

	bucket := spec.BucketOrDefault()
	labels := labelsFor(dp, componentMinio)
	job = &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameMinioBucketJob,
			Namespace: ns,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(int32(6)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:  "mc",
						Image: spec.McImageOrDefault(),
						Args:  []string{"mb", "--ignore-existing", "local/" + bucket},
						Env: []corev1.EnvVar{{
							Name: keyMCHost,
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: secretMinioMC},
									Key:                  keyMCHost,
								},
							},
						}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}
	if err := r.apply(ctx, dp, job, func() error { return nil }); err != nil {
		return false, err
	}
	return false, nil
}

func jobSucceeded(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return job.Status.Succeeded > 0
}

func jobFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
