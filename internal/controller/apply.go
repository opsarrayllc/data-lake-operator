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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

const (
	managedBy                 = "data-platform-operator"
	labelAppName              = "app.kubernetes.io/name"
	labelAppInstance          = "app.kubernetes.io/instance"
	labelAppComponent         = "app.kubernetes.io/component"
	labelAppManagedBy         = "app.kubernetes.io/managed-by"
	labelPlatform             = "dataplatform.opsarray.io/name"
	annotationConfigHash      = "dataplatform.opsarray.io/config-hash"
	componentMinio            = "minio"
	componentPostgres         = "postgres"
	componentLakekeeper       = "lakekeeper"
	componentTrinoCoordinator = "trino-coordinator"
	componentTrinoWorker      = "trino-worker"
	componentKeycloak         = "keycloak"
	nameMinio                 = "minio"
	nameMinioBucketJob        = "minio-create-bucket"
	namePostgres              = "postgres"
	nameLakekeeper            = "lakekeeper"
	nameTrino                 = "trino"
	nameTrinoWorker           = "trino-worker"
	nameKeycloak              = "keycloak"
	secretMinio               = "minio"
	secretMinioMC             = "minio-mc"
	secretPostgres            = "postgres"
	secretEncryption          = "lakekeeper-encryption"
	secretTrinoCatalog        = "trino-catalog-lakekeeper"
	secretKeycloakAdmin       = "keycloak-admin"
	secretOIDC                = "oidc"
	configMapTrino            = "trino-config"
	configMapKeycloakRealm    = "keycloak-realm"
	keyEncryption             = "LAKEKEEPER__PG_ENCRYPTION_KEY"
	keyS3Access               = "AWS_ACCESS_KEY_ID"
	keyS3Secret               = "AWS_SECRET_ACCESS_KEY"
	keyMCHost                 = "MC_HOST_local"
	keyPostgresUsername       = "username"
	keyPostgresPassword       = "password"
	keyPostgresDatabase       = "database"
	keyOIDCClientID           = "clientID"
	keyOIDCClientSecret       = "clientSecret"
	keyOIDCTrinoClientID      = "trinoClientID"
	keyOIDCTrinoClientSecret  = "trinoClientSecret"
	keyOIDCOperatorClientID   = "operatorClientID"
	keyOIDCOperatorSecret     = "operatorClientSecret"
	keyKeycloakAdminUser      = "username"
	keyKeycloakAdminPassword  = "password"
	keyRealmJSON              = "realm.json"
	portNameHTTP              = "http"
	sslModeDisable            = "disable"
	volumeData                = "data"
	volumeTmp                 = "tmp"
	volumeRealm               = "realm"
	volumeConfig              = "config"
	keyHome                   = "HOME"
	keyMcConfigDir            = "MC_CONFIG_DIR"
	mcHomePath                = "/tmp"
	mcConfigPath              = "/tmp/.mc"
	keyPGDATA                 = "PGDATA"
	pgDataMountPath           = "/var/lib/postgresql/data"
	pgDataPath                = "/var/lib/postgresql/data/pgdata"
	minioAPIPort              = int32(9000)
	minioConsolePort          = int32(9001)
	postgresPort              = int32(5432)
	lakekeeperPort            = int32(8181)
	trinoPort                 = int32(8080)
	keycloakPort              = int32(8080)
	uidMinio                  = int64(1000)
	gidMinio                  = int64(1000)
	uidPostgres               = int64(999)
	gidPostgres               = int64(999)
	uidLakekeeper             = int64(65532)
	gidLakekeeper             = int64(65534)
	uidTrino                  = int64(1000)
	gidTrino                  = int64(1000)
	uidKeycloak               = int64(1000)
	gidKeycloak               = int64(1000)
	reasonReconciling         = "Reconciling"
	reasonReady               = "Ready"
	reasonNotReady            = "NotReady"
	reasonError               = "Error"
	reasonDisabled            = "Disabled"
	reasonMissing             = "Missing"
)

func labelsFor(dp *dataplatformv1alpha1.DataPlatform, component string) map[string]string {
	name := component
	switch component {
	case componentTrinoCoordinator, componentTrinoWorker:
		name = nameTrino
	}
	return map[string]string{
		labelAppName:      name,
		labelAppInstance:  dp.Name,
		labelAppComponent: component,
		labelAppManagedBy: managedBy,
		labelPlatform:     dp.Name,
	}
}

func ensureLabels(obj metav1.Object, labels map[string]string) {
	current := obj.GetLabels()
	if current == nil {
		current = map[string]string{}
	}
	maps.Copy(current, labels)
	obj.SetLabels(current)
}

func clusterServiceURL(name, namespace string, port int32) string {
	return fmt.Sprintf("http://%s.%s.svc:%d", name, namespace, port)
}

func hashData(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func setCondition(dp *dataplatformv1alpha1.DataPlatform, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&dp.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: dp.Generation,
	})
}

func conditionTrue(dp *dataplatformv1alpha1.DataPlatform, condType string) bool {
	return meta.IsStatusConditionTrue(dp.Status.Conditions, condType)
}

func (r *DataPlatformReconciler) apply(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	obj client.Object,
	mutate func() error,
) error {
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
		if err := mutate(); err != nil {
			return err
		}
		return controllerutil.SetControllerReference(dp, obj, r.Scheme)
	})
	return err
}

func (r *DataPlatformReconciler) ensureGeneratedSecret(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	name, namespace string,
	component string,
	data map[string][]byte,
) error {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: name, Namespace: namespace}
	err := r.Get(ctx, key, secret)
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return err
	}
	secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labelsFor(dp, component),
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
	if err := controllerutil.SetControllerReference(dp, secret, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, secret)
}

func (r *DataPlatformReconciler) getSecretData(ctx context.Context, name, namespace, key string) (string, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret); err != nil {
		return "", err
	}
	val, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s is missing key %s", namespace, name, key)
	}
	return string(val), nil
}

func restrictedPodSecurity(uid, gid int64) *corev1.PodSecurityContext {
	sc := &corev1.PodSecurityContext{
		RunAsNonRoot:   ptr.To(true),
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
	if uid > 0 {
		sc.RunAsUser = ptr.To(uid)
	}
	if gid > 0 {
		sc.RunAsGroup = ptr.To(gid)
		sc.FSGroup = ptr.To(gid)
		sc.FSGroupChangePolicy = ptr.To(corev1.FSGroupChangeOnRootMismatch)
	}
	return sc
}

func restrictedContainerSecurity(uid, gid int64) *corev1.SecurityContext {
	sc := &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		RunAsNonRoot:             ptr.To(true),
	}
	if uid > 0 {
		sc.RunAsUser = ptr.To(uid)
	}
	if gid > 0 {
		sc.RunAsGroup = ptr.To(gid)
	}
	return sc
}

func (r *DataPlatformReconciler) patchStatus(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform) error {
	latest := &dataplatformv1alpha1.DataPlatform{}
	if err := r.Get(ctx, types.NamespacedName{Name: dp.Name}, latest); err != nil {
		return err
	}
	latest.Status = dp.Status
	return r.Status().Update(ctx, latest)
}
