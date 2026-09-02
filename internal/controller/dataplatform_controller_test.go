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
	"net/http"
	"net/url"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

type fakeCatalog struct {
	bootstraps int
	warehouses []string
}

func (f *fakeCatalog) Bootstrap(_ context.Context, _, _ string, _ int32, _ string) error {
	f.bootstraps++
	return nil
}

func (f *fakeCatalog) EnsureWarehouse(_ context.Context, _, _ string, _ int32, _ string, req WarehouseRequest) error {
	f.warehouses = append(f.warehouses, req.Name)
	return nil
}

func (f *fakeCatalog) FormPost(_ context.Context, _, _ string, _ int32, _ string, _ url.Values) (int, []byte, error) {
	return http.StatusOK, []byte(`{"access_token":"fake"}`), nil
}

func (f *fakeCatalog) FetchToken(_ context.Context, _ string, _ url.Values) (int, []byte, error) {
	return http.StatusOK, []byte(`{"access_token":"fake"}`), nil
}

var _ = Describe("DataPlatform Controller", func() {
	const resourceName = "test-platform"

	ctx := context.Background()
	typeNamespacedName := types.NamespacedName{Name: resourceName}

	var (
		catalog    *fakeCatalog
		reconciler *DataPlatformReconciler
	)

	BeforeEach(func() {
		catalog = &fakeCatalog{}
		reconciler = &DataPlatformReconciler{
			Client:  k8sClient,
			Scheme:  k8sClient.Scheme(),
			Catalog: catalog,
		}

		By("creating the DataPlatform")
		existing := &dataplatformv1alpha1.DataPlatform{}
		err := k8sClient.Get(ctx, typeNamespacedName, existing)
		if err != nil && errors.IsNotFound(err) {
			resource := &dataplatformv1alpha1.DataPlatform{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		}
	})

	AfterEach(func() {
		resource := &dataplatformv1alpha1.DataPlatform{}
		err := k8sClient.Get(ctx, typeNamespacedName, resource)
		if errors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
	})

	It("creates MinIO, Keycloak, Postgres, LakeKeeper, and Trino resources", func() {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		By("creating MinIO")
		sts := &appsv1.StatefulSet{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nameMinio, Namespace: nameMinio}, sts)).To(Succeed())
		Expect(sts.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser).To(Equal(ptr.To(uidMinio)))
		svc := &corev1.Service{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nameMinio, Namespace: nameMinio}, svc)).To(Succeed())

		By("creating Keycloak")
		deploy := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nameKeycloak, Namespace: nameKeycloak}, deploy)).To(Succeed())
		Expect(deploy.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser).To(Equal(ptr.To(uidKeycloak)))
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: secretOIDC, Namespace: nameKeycloak}, &corev1.Secret{})).To(Succeed())

		By("creating the LakeKeeper namespace workloads")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: namePostgres, Namespace: nameLakekeeper}, sts)).To(Succeed())
		Expect(sts.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser).To(Equal(ptr.To(uidPostgres)))
		Expect(sts.Spec.Template.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: keyPGDATA, Value: pgDataPath}))

		By("waiting for Keycloak before LakeKeeper")
		err = k8sClient.Get(ctx, types.NamespacedName{Name: nameLakekeeper, Namespace: nameLakekeeper}, deploy)
		Expect(errors.IsNotFound(err)).To(BeTrue())

		markDeploymentReady(ctx, nameKeycloak, nameKeycloak)
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nameLakekeeper, Namespace: nameLakekeeper}, deploy)).To(Succeed())
		Expect(deploy.Spec.Template.Spec.InitContainers).To(HaveLen(2))
		Expect(deploy.Spec.Template.Spec.InitContainers[0].SecurityContext.RunAsUser).To(Equal(ptr.To(uidPostgres)))
		Expect(deploy.Spec.Template.Spec.InitContainers[1].SecurityContext.RunAsUser).To(Equal(ptr.To(uidLakekeeper)))
		Expect(deploy.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser).To(Equal(ptr.To(uidLakekeeper)))
		Expect(envValue(deploy.Spec.Template.Spec.Containers[0].Env, "LAKEKEEPER__OPENID_PROVIDER_URI")).To(ContainSubstring("/realms/dataplatform"))
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nameLakekeeper, Namespace: nameLakekeeper}, svc)).To(Succeed())

		By("creating the Trino coordinator wired to MinIO and OIDC")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nameTrino, Namespace: nameTrino}, deploy)).To(Succeed())
		Expect(deploy.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser).To(Equal(ptr.To(uidTrino)))
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nameTrino, Namespace: nameTrino}, svc)).To(Succeed())
		catalogSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: secretTrinoCatalog, Namespace: nameTrino}, catalogSecret)).To(Succeed())
		props := string(catalogSecret.Data[nameLakekeeper+".properties"])
		Expect(props).To(ContainSubstring("iceberg.catalog.type=rest"))
		Expect(props).To(ContainSubstring("s3.endpoint=http://minio.minio.svc:9000"))
		Expect(props).To(ContainSubstring("s3.path-style-access=true"))
		Expect(props).To(ContainSubstring("iceberg.rest-catalog.security=OAUTH2"))
		Expect(props).To(ContainSubstring("iceberg.rest-catalog.oauth2.server-uri="))

		By("not bootstrapping until LakeKeeper and MinIO are ready")
		Expect(catalog.bootstraps).To(Equal(0))
	})

	It("bootstraps the warehouse once LakeKeeper and MinIO are ready", func() {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		markStatefulSetReady(ctx, namePostgres, nameLakekeeper)
		markStatefulSetReady(ctx, nameMinio, nameMinio)
		markDeploymentReady(ctx, nameKeycloak, nameKeycloak)

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		markDeploymentReady(ctx, nameLakekeeper, nameLakekeeper)

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		job := &batchv1.Job{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nameMinioBucketJob, Namespace: nameMinio}, job)).To(Succeed())
		Expect(job.Spec.Template.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: keyHome, Value: mcHomePath}))
		Expect(job.Spec.Template.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: keyMcConfigDir, Value: mcConfigPath}))
		job.Status.Succeeded = 1
		Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(catalog.bootstraps).To(Equal(1))
		Expect(catalog.warehouses).To(ContainElement("default"))

		updated := &dataplatformv1alpha1.DataPlatform{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
		Expect(updated.Status.MinioEndpoint).To(Equal("http://minio.minio.svc:9000"))
		Expect(updated.Status.LakekeeperEndpoint).To(Equal("http://lakekeeper.lakekeeper.svc:8181"))
		Expect(updated.Status.TrinoEndpoint).To(Equal("http://trino.trino.svc:8080"))
		Expect(updated.Status.KeycloakEndpoint).To(Equal("http://keycloak.keycloak.svc:8080"))
	})

	It("uses an external S3 store when embedded is false", func() {
		resource := &dataplatformv1alpha1.DataPlatform{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
		Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			return errors.IsNotFound(err)
		}).Should(BeTrue())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nameLakekeeper}}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: ns.Name}, ns)
		if err != nil && errors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "s3-credentials", Namespace: nameLakekeeper},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				keyS3Access: []byte("test-access"),
				keyS3Secret: []byte("test-secret"),
			},
		}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, secret)
		if err != nil && errors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		}

		external := &dataplatformv1alpha1.DataPlatform{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName},
			Spec: dataplatformv1alpha1.DataPlatformSpec{
				Storage: dataplatformv1alpha1.StorageSpec{
					Embedded: ptr.To(false),
					S3: &dataplatformv1alpha1.S3Spec{
						Bucket: "test-bucket",
						Region: "us-east-1",
						CredentialsSecretRef: &dataplatformv1alpha1.S3CredentialsSecretRef{
							Name:      "s3-credentials",
							Namespace: nameLakekeeper,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, external)).To(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		catalogSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: secretTrinoCatalog, Namespace: nameTrino}, catalogSecret)).To(Succeed())
		Expect(string(catalogSecret.Data[nameLakekeeper+".properties"])).To(ContainSubstring("s3.aws-access-key=test-access"))
		Expect(string(catalogSecret.Data[nameLakekeeper+".properties"])).NotTo(ContainSubstring("s3.path-style-access=true"))
	})

	It("uses an external OIDC issuer when auth.embedded is false", func() {
		resource := &dataplatformv1alpha1.DataPlatform{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
		Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			return errors.IsNotFound(err)
		}).Should(BeTrue())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nameLakekeeper}}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: ns.Name}, ns)
		if err != nil && errors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "oidc-credentials", Namespace: nameLakekeeper},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				keyOIDCClientSecret: []byte("okta-secret"),
			},
		}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, secret)
		if err != nil && errors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		}

		external := &dataplatformv1alpha1.DataPlatform{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName},
			Spec: dataplatformv1alpha1.DataPlatformSpec{
				Auth: dataplatformv1alpha1.AuthSpec{
					Embedded: ptr.To(false),
					OIDC: &dataplatformv1alpha1.OIDCSpec{
						Issuer:        "https://example.okta.com/oauth2/default",
						TokenEndpoint: "https://example.okta.com/oauth2/default/v1/token",
						Audience:      "api://lakekeeper",
						ClientID:      "lakekeeper",
						Scope:         "openid",
						CredentialsSecretRef: &dataplatformv1alpha1.OIDCCredentialsSecretRef{
							Name:      "oidc-credentials",
							Namespace: nameLakekeeper,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, external)).To(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		deploy := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nameLakekeeper, Namespace: nameLakekeeper}, deploy)).To(Succeed())
		Expect(envValue(deploy.Spec.Template.Spec.Containers[0].Env, "LAKEKEEPER__OPENID_PROVIDER_URI")).To(Equal("https://example.okta.com/oauth2/default"))
		Expect(envValue(deploy.Spec.Template.Spec.Containers[0].Env, "LAKEKEEPER__OPENID_AUDIENCE")).To(Equal("api://lakekeeper"))

		catalogSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: secretTrinoCatalog, Namespace: nameTrino}, catalogSecret)).To(Succeed())
		props := string(catalogSecret.Data[nameLakekeeper+".properties"])
		Expect(props).To(ContainSubstring("iceberg.rest-catalog.security=OAUTH2"))
		Expect(props).To(ContainSubstring("iceberg.rest-catalog.oauth2.server-uri=https://example.okta.com/oauth2/default/v1/token"))
		Expect(props).To(ContainSubstring("iceberg.rest-catalog.oauth2.credential=lakekeeper:okta-secret"))
	})
})

func markStatefulSetReady(ctx context.Context, name, ns string) {
	sts := &appsv1.StatefulSet{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, sts)).To(Succeed())
	sts.Status.ReadyReplicas = 1
	sts.Status.Replicas = 1
	Expect(k8sClient.Status().Update(ctx, sts)).To(Succeed())
}

func markDeploymentReady(ctx context.Context, name, ns string) {
	deploy := &appsv1.Deployment{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, deploy)).To(Succeed())
	deploy.Status.ReadyReplicas = 1
	deploy.Status.Replicas = 1
	Expect(k8sClient.Status().Update(ctx, deploy)).To(Succeed())
}

func envValue(env []corev1.EnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}
