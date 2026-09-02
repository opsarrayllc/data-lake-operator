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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// ConditionReady is True when every enabled component is ready.
	ConditionReady = "Ready"
	// ConditionPostgresReady is True when LakeKeeper's Postgres is usable.
	ConditionPostgresReady = "PostgresReady"
	// ConditionLakekeeperReady is True when the LakeKeeper Deployment is ready.
	ConditionLakekeeperReady = "LakekeeperReady"
	// ConditionWarehouseReady is True when the Iceberg warehouse exists.
	ConditionWarehouseReady = "WarehouseReady"
	// ConditionTrinoReady is True when the Trino coordinator is ready.
	ConditionTrinoReady = "TrinoReady"
	// ConditionMinioReady is True when in-cluster MinIO is usable, or when using an external store.
	ConditionMinioReady = "MinioReady"
	// ConditionAuthReady is True when the identity provider is usable, or when auth is disabled.
	ConditionAuthReady = "AuthReady"

	DefaultLakekeeperNamespace = "lakekeeper"
	DefaultTrinoNamespace      = "trino"
	DefaultMinioNamespace      = "minio"
	DefaultKeycloakNamespace   = "keycloak"
	DefaultLakekeeperImage     = "quay.io/lakekeeper/catalog:v0.13.3"
	DefaultTrinoImage          = "trinodb/trino:476"
	DefaultPostgresImage       = "postgres:17"
	DefaultMinioImage          = "minio/minio:RELEASE.2025-04-22T22-12-26Z"
	DefaultMcImage             = "minio/mc:RELEASE.2025-04-16T18-13-26Z"
	DefaultKeycloakImage       = "quay.io/keycloak/keycloak:26.3.3"
	DefaultWarehouseName       = "default"
	DefaultPostgresStorage     = "10Gi"
	DefaultMinioStorage        = "20Gi"
	DefaultMinioBucket         = "warehouse"
	DefaultS3Flavor            = "aws"
	DefaultS3CompatFlavor      = "s3-compat"
	DefaultS3CompatRegion      = "us-east-1"
	DefaultOIDCRealm           = "dataplatform"
	DefaultOIDCAudience        = "lakekeeper"
	DefaultOIDCClientID        = "lakekeeper"
	DefaultOIDCTrinoClientID   = "trino"
	DefaultOIDCOperatorClient  = "operator"
	DefaultOIDCScope           = "lakekeeper"
	DefaultOIDCAdminUser       = "admin"
)

// DataPlatformSpec defines the desired state of DataPlatform.
type DataPlatformSpec struct {
	// storage configures the object store used by the Iceberg warehouse.
	// Required when LakeKeeper is enabled.
	// +optional
	Storage StorageSpec `json:"storage"`

	// auth configures OIDC. By default the operator deploys Keycloak for local
	// use. Set embedded to false and fill oidc to use Okta, JumpCloud, or another IdP.
	// +optional
	Auth AuthSpec `json:"auth"`

	// lakekeeper configures the Iceberg REST catalog.
	// +optional
	Lakekeeper LakekeeperSpec `json:"lakekeeper"`

	// trino configures the query engine.
	// +optional
	Trino TrinoSpec `json:"trino"`
}

// AuthSpec configures identity for LakeKeeper and Trino.
// By default the operator deploys Keycloak. Set embedded to false and fill oidc
// to use an existing provider such as Okta or JumpCloud.
type AuthSpec struct {
	// enabled turns on OIDC for LakeKeeper and the Trino Iceberg catalog.
	// Defaults to true.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// embedded deploys Keycloak in its own namespace. Defaults to true when auth is enabled.
	// +optional
	Embedded *bool `json:"embedded,omitempty"`

	// keycloak configures the operator-managed Keycloak instance.
	// +optional
	Keycloak KeycloakSpec `json:"keycloak"`

	// oidc is required when embedded is false.
	// +optional
	OIDC *OIDCSpec `json:"oidc,omitempty"`
}

// KeycloakSpec configures operator-managed Keycloak.
type KeycloakSpec struct {
	// namespace defaults to "keycloak". Use a unique value if you create multiple DataPlatforms.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// image is the Keycloak container image.
	// +optional
	Image string `json:"image,omitempty"`

	// realm is imported on first start. Defaults to "dataplatform".
	// +optional
	Realm string `json:"realm,omitempty"`

	// publicURL is the Keycloak URL browsers use (for example http://localhost:8080 after port-forward).
	// LakeKeeper's UI redirect uses this when set. In-cluster services always use the cluster DNS issuer.
	// +optional
	PublicURL string `json:"publicURL,omitempty"`

	// resources are compute resource requirements for the Keycloak container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// OIDCSpec points at an existing OpenID Connect provider (Okta, JumpCloud, Keycloak, ...).
type OIDCSpec struct {
	// issuer is the OIDC issuer URL (no trailing slash).
	// Example (Okta): https://example.okta.com/oauth2/default
	// Example (JumpCloud / Keycloak): https://idp.example.com/realms/company
	// +kubebuilder:validation:MinLength=1
	Issuer string `json:"issuer"`

	// audience is the expected JWT aud claim. Defaults to "lakekeeper".
	// +optional
	Audience string `json:"audience,omitempty"`

	// clientID is the OIDC client used by the LakeKeeper UI and, unless overridden
	// in the credentials Secret, by Trino and the operator.
	// +optional
	ClientID string `json:"clientID,omitempty"`

	// tokenEndpoint is the OAuth2 token URL. Defaults to issuer + "/protocol/openid-connect/token"
	// (Keycloak / JumpCloud). Okta typically needs "/v1/token" on the authorization server.
	// +optional
	TokenEndpoint string `json:"tokenEndpoint,omitempty"`

	// scope is requested during the client-credentials grant. Defaults to "lakekeeper".
	// +optional
	Scope string `json:"scope,omitempty"`

	// credentialsSecretRef locates the client secret (and optional per-component client IDs).
	// Namespace is required because DataPlatform is cluster-scoped.
	// +optional
	CredentialsSecretRef *OIDCCredentialsSecretRef `json:"credentialsSecretRef,omitempty"`
}

// OIDCCredentialsSecretRef locates a Secret with OIDC client credentials.
type OIDCCredentialsSecretRef struct {
	// name of the Secret.
	Name string `json:"name"`

	// namespace of the Secret.
	Namespace string `json:"namespace"`

	// clientSecretKey is the Secret key for the client secret.
	// +optional
	// +kubebuilder:default=clientSecret
	ClientSecretKey string `json:"clientSecretKey,omitempty"`

	// clientIDKey, if set, overrides spec.auth.oidc.clientID from the Secret.
	// +optional
	ClientIDKey string `json:"clientIDKey,omitempty"`

	// trinoClientIDKey, if set, uses a separate confidential client for Trino.
	// +optional
	TrinoClientIDKey string `json:"trinoClientIDKey,omitempty"`

	// trinoClientSecretKey, if set, uses a separate secret for the Trino client.
	// +optional
	TrinoClientSecretKey string `json:"trinoClientSecretKey,omitempty"`
}

// StorageSpec configures table data storage.
// By default the operator deploys MinIO. Set embedded to false and fill s3
// to use AWS S3 or another existing S3-compatible store.
type StorageSpec struct {
	// embedded deploys MinIO in its own namespace. Defaults to true.
	// +optional
	Embedded *bool `json:"embedded,omitempty"`

	// minio configures the operator-managed MinIO instance.
	// +optional
	Minio MinioSpec `json:"minio"`

	// s3 is required when embedded is false.
	// +optional
	S3 *S3Spec `json:"s3,omitempty"`
}

// MinioSpec configures operator-managed MinIO.
type MinioSpec struct {
	// namespace defaults to "minio". Use a unique value if you create multiple DataPlatforms.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// image is the MinIO server image.
	// +optional
	Image string `json:"image,omitempty"`

	// mcImage is the MinIO client image used to create the bucket.
	// +optional
	McImage string `json:"mcImage,omitempty"`

	// storageSize is the PVC size.
	// +optional
	StorageSize string `json:"storageSize,omitempty"`

	// bucket is created in MinIO if it does not exist.
	// +optional
	Bucket string `json:"bucket,omitempty"`

	// resources are compute resource requirements for the MinIO container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// S3Spec is the warehouse object-store profile.
type S3Spec struct {
	// bucket is the S3 bucket name.
	// +kubebuilder:validation:MinLength=1
	Bucket string `json:"bucket"`

	// region is the AWS region, or a placeholder such as "local" for S3-compatible stores.
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// endpoint is an optional custom S3 API URL (MinIO, SeaweedFS, etc.).
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// pathStyleAccess uses path-style URLs. Required for many S3-compatible stores.
	// +optional
	PathStyleAccess bool `json:"pathStyleAccess,omitempty"`

	// flavor selects the LakeKeeper storage profile.
	// +optional
	// +kubebuilder:validation:Enum=aws;s3-compat
	// +kubebuilder:default=aws
	Flavor string `json:"flavor,omitempty"`

	// stsEnabled asks LakeKeeper to vend temporary credentials to engines.
	// +optional
	STSEnabled bool `json:"stsEnabled,omitempty"`

	// stsRoleARN is the IAM role LakeKeeper assumes when STS is enabled.
	// +optional
	STSRoleARN string `json:"stsRoleARN,omitempty"`

	// credentialsSecretRef points at a Secret with static S3 keys.
	// Namespace is required because DataPlatform is cluster-scoped.
	// Required when using an external store (storage.embedded=false).
	// +optional
	CredentialsSecretRef *S3CredentialsSecretRef `json:"credentialsSecretRef,omitempty"`
}

// S3CredentialsSecretRef locates a Secret that holds AWS-style access keys.
type S3CredentialsSecretRef struct {
	// name of the Secret.
	Name string `json:"name"`

	// namespace of the Secret.
	Namespace string `json:"namespace"`

	// accessKeyIDKey is the Secret key for the access key ID.
	// +optional
	// +kubebuilder:default=AWS_ACCESS_KEY_ID
	AccessKeyIDKey string `json:"accessKeyIDKey,omitempty"`

	// secretAccessKeyKey is the Secret key for the secret access key.
	// +optional
	// +kubebuilder:default=AWS_SECRET_ACCESS_KEY
	SecretAccessKeyKey string `json:"secretAccessKeyKey,omitempty"`
}

// LakekeeperSpec configures the Iceberg REST catalog.
type LakekeeperSpec struct {
	// enabled deploys LakeKeeper. Defaults to true.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// namespace is the Kubernetes namespace for LakeKeeper (and embedded Postgres).
	// Defaults to "lakekeeper". Use a unique value if you create multiple DataPlatforms.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// image is the LakeKeeper container image.
	// +optional
	Image string `json:"image,omitempty"`

	// replicas is the number of LakeKeeper pods.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// resources are compute resource requirements for the catalog container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// extraEnv is appended to the LakeKeeper container (LAKEKEEPER__* and others).
	// +optional
	ExtraEnv []corev1.EnvVar `json:"extraEnv,omitempty"`

	// postgres is the catalog metadata database.
	// +optional
	Postgres PostgresSpec `json:"postgres"`

	// warehouse is created via the LakeKeeper management API after bootstrap.
	// +optional
	Warehouse WarehouseSpec `json:"warehouse"`
}

// PostgresSpec is either an operator-managed instance or a pointer at an existing database.
type PostgresSpec struct {
	// embedded deploys a single-replica Postgres in the LakeKeeper namespace.
	// Defaults to true. Set false and fill host/credentials for an external database.
	// +optional
	Embedded *bool `json:"embedded,omitempty"`

	// image is used when embedded is true.
	// +optional
	Image string `json:"image,omitempty"`

	// storageSize is the PVC size for embedded Postgres.
	// +optional
	StorageSize string `json:"storageSize,omitempty"`

	// host is required when embedded is false.
	// +optional
	Host string `json:"host,omitempty"`

	// port of the external database.
	// +optional
	Port int32 `json:"port,omitempty"`

	// database name.
	// +optional
	Database string `json:"database,omitempty"`

	// sslMode is passed to the Postgres driver (disable, prefer, require, ...).
	// +optional
	SSLMode string `json:"sslMode,omitempty"`

	// credentialsSecretRef locates username/password for an external database.
	// +optional
	CredentialsSecretRef *PostgresCredentialsSecretRef `json:"credentialsSecretRef,omitempty"`
}

// PostgresCredentialsSecretRef locates a Secret with username and password.
type PostgresCredentialsSecretRef struct {
	// name of the Secret.
	Name string `json:"name"`

	// namespace of the Secret. Defaults to the LakeKeeper namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// usernameKey is the Secret key for the database user.
	// +optional
	// +kubebuilder:default=username
	UsernameKey string `json:"usernameKey,omitempty"`

	// passwordKey is the Secret key for the database password.
	// +optional
	// +kubebuilder:default=password
	PasswordKey string `json:"passwordKey,omitempty"`
}

// WarehouseSpec is the LakeKeeper warehouse created for Iceberg tables.
type WarehouseSpec struct {
	// name is the warehouse identifier engines use in iceberg.rest-catalog.warehouse.
	// +optional
	Name string `json:"name,omitempty"`

	// keyPrefix is an optional path inside the bucket.
	// +optional
	KeyPrefix string `json:"keyPrefix,omitempty"`
}

// TrinoSpec configures the query engine.
type TrinoSpec struct {
	// enabled deploys Trino. Defaults to true.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// namespace is the Kubernetes namespace for Trino.
	// Defaults to "trino". Use a unique value if you create multiple DataPlatforms.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// image is the Trino container image.
	// +optional
	Image string `json:"image,omitempty"`

	// workers is the number of Trino worker pods. Zero runs a coordinator-only cluster.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Workers *int32 `json:"workers,omitempty"`

	// coordinator configures the Trino coordinator.
	// +optional
	Coordinator TrinoCoordinatorSpec `json:"coordinator"`

	// extraConfig is merged into config.properties (key=value).
	// +optional
	ExtraConfig map[string]string `json:"extraConfig,omitempty"`

	// extraCatalogs adds extra files under etc/catalog. The "lakekeeper" catalog is reserved.
	// +optional
	ExtraCatalogs map[string]string `json:"extraCatalogs,omitempty"`

	// extraEnv is appended to Trino containers.
	// +optional
	ExtraEnv []corev1.EnvVar `json:"extraEnv,omitempty"`

	// resources are compute resource requirements for worker pods.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// service exposes the coordinator.
	// +optional
	Service ServiceSpec `json:"service"`
}

// TrinoCoordinatorSpec configures the coordinator pod.
type TrinoCoordinatorSpec struct {
	// resources are compute resource requirements for the coordinator container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// ServiceSpec configures a Kubernetes Service.
type ServiceSpec struct {
	// type is the Service type.
	// +optional
	// +kubebuilder:default=ClusterIP
	Type corev1.ServiceType `json:"type,omitempty"`
}

// DataPlatformStatus defines the observed state of DataPlatform.
type DataPlatformStatus struct {
	// conditions represent the current state of the DataPlatform resource.
	//
	// Standard condition types include:
	// - "Ready": every enabled component is fully functional
	// - "MinioReady": in-cluster MinIO is ready, or an external object store is configured
	// - "AuthReady": Keycloak is ready, an external OIDC issuer is configured, or auth is disabled
	// - "PostgresReady": the catalog database is usable
	// - "LakekeeperReady": the catalog Deployment is ready
	// - "WarehouseReady": the Iceberg warehouse has been created
	// - "TrinoReady": the Trino coordinator is ready
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// minioEndpoint is the in-cluster S3 API URL when MinIO is embedded.
	// +optional
	MinioEndpoint string `json:"minioEndpoint,omitempty"`

	// lakekeeperEndpoint is the in-cluster HTTP URL of the catalog.
	// +optional
	LakekeeperEndpoint string `json:"lakekeeperEndpoint,omitempty"`

	// trinoEndpoint is the in-cluster HTTP URL of the Trino coordinator.
	// +optional
	TrinoEndpoint string `json:"trinoEndpoint,omitempty"`

	// keycloakEndpoint is the in-cluster HTTP URL of Keycloak when it is embedded.
	// +optional
	KeycloakEndpoint string `json:"keycloakEndpoint,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Lakekeeper",type=string,JSONPath=".status.lakekeeperEndpoint"
// +kubebuilder:printcolumn:name="Trino",type=string,JSONPath=".status.trinoEndpoint"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// DataPlatform is the Schema for the dataplatforms API.
type DataPlatform struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DataPlatform
	// +required
	Spec DataPlatformSpec `json:"spec"`

	// status defines the observed state of DataPlatform
	// +optional
	Status DataPlatformStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DataPlatformList contains a list of DataPlatform
type DataPlatformList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DataPlatform `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DataPlatform{}, &DataPlatformList{})
		return nil
	})
}
