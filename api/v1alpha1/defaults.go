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
	"strings"

	corev1 "k8s.io/api/core/v1"
)

func boolDefaultTrue(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func int32OrDefault(v *int32, def int32) int32 {
	if v == nil {
		return def
	}
	return *v
}

func stringOrDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// IsEnabled reports whether LakeKeeper should be deployed. Nil defaults to true.
func (s *LakekeeperSpec) IsEnabled() bool {
	return boolDefaultTrue(s.Enabled)
}

// NamespaceOrDefault returns the LakeKeeper namespace.
func (s *LakekeeperSpec) NamespaceOrDefault() string {
	return stringOrDefault(s.Namespace, DefaultLakekeeperNamespace)
}

// ImageOrDefault returns the LakeKeeper image.
func (s *LakekeeperSpec) ImageOrDefault() string {
	return stringOrDefault(s.Image, DefaultLakekeeperImage)
}

// ReplicasOrDefault returns the LakeKeeper replica count.
func (s *LakekeeperSpec) ReplicasOrDefault() int32 {
	return int32OrDefault(s.Replicas, 1)
}

// IsEmbedded reports whether Postgres should be operator-managed. Nil defaults to true.
func (s *PostgresSpec) IsEmbedded() bool {
	return boolDefaultTrue(s.Embedded)
}

// ImageOrDefault returns the embedded Postgres image.
func (s *PostgresSpec) ImageOrDefault() string {
	return stringOrDefault(s.Image, DefaultPostgresImage)
}

// StorageSizeOrDefault returns the embedded Postgres PVC size.
func (s *PostgresSpec) StorageSizeOrDefault() string {
	return stringOrDefault(s.StorageSize, DefaultPostgresStorage)
}

// PortOrDefault returns the Postgres port.
func (s *PostgresSpec) PortOrDefault() int32 {
	if s.Port == 0 {
		return 5432
	}
	return s.Port
}

// DatabaseOrDefault returns the Postgres database name.
func (s *PostgresSpec) DatabaseOrDefault() string {
	return stringOrDefault(s.Database, "postgres")
}

// NameOrDefault returns the warehouse name.
func (s *WarehouseSpec) NameOrDefault() string {
	return stringOrDefault(s.Name, DefaultWarehouseName)
}

// IsEnabled reports whether Trino should be deployed. Nil defaults to true.
func (s *TrinoSpec) IsEnabled() bool {
	return boolDefaultTrue(s.Enabled)
}

// NamespaceOrDefault returns the Trino namespace.
func (s *TrinoSpec) NamespaceOrDefault() string {
	return stringOrDefault(s.Namespace, DefaultTrinoNamespace)
}

// ImageOrDefault returns the Trino image.
func (s *TrinoSpec) ImageOrDefault() string {
	return stringOrDefault(s.Image, DefaultTrinoImage)
}

// WorkersOrDefault returns the Trino worker count.
func (s *TrinoSpec) WorkersOrDefault() int32 {
	return int32OrDefault(s.Workers, 0)
}

// TypeOrDefault returns the Service type.
func (s *ServiceSpec) TypeOrDefault() corev1.ServiceType {
	if s.Type == "" {
		return corev1.ServiceTypeClusterIP
	}
	return s.Type
}

// IsEmbedded reports whether MinIO should be operator-managed. Nil defaults to true.
func (s *StorageSpec) IsEmbedded() bool {
	return boolDefaultTrue(s.Embedded)
}

// NamespaceOrDefault returns the MinIO namespace.
func (s *MinioSpec) NamespaceOrDefault() string {
	return stringOrDefault(s.Namespace, DefaultMinioNamespace)
}

// ImageOrDefault returns the MinIO server image.
func (s *MinioSpec) ImageOrDefault() string {
	return stringOrDefault(s.Image, DefaultMinioImage)
}

// McImageOrDefault returns the MinIO client image.
func (s *MinioSpec) McImageOrDefault() string {
	return stringOrDefault(s.McImage, DefaultMcImage)
}

// StorageSizeOrDefault returns the MinIO PVC size.
func (s *MinioSpec) StorageSizeOrDefault() string {
	return stringOrDefault(s.StorageSize, DefaultMinioStorage)
}

// BucketOrDefault returns the MinIO bucket name.
func (s *MinioSpec) BucketOrDefault() string {
	return stringOrDefault(s.Bucket, DefaultMinioBucket)
}

// FlavorOrDefault returns the S3 storage flavor.
func (s *S3Spec) FlavorOrDefault() string {
	return stringOrDefault(s.Flavor, DefaultS3Flavor)
}

// AccessKeyIDKeyOrDefault returns the Secret key for the access key ID.
func (s *S3CredentialsSecretRef) AccessKeyIDKeyOrDefault() string {
	return stringOrDefault(s.AccessKeyIDKey, "AWS_ACCESS_KEY_ID")
}

// SecretAccessKeyKeyOrDefault returns the Secret key for the secret access key.
func (s *S3CredentialsSecretRef) SecretAccessKeyKeyOrDefault() string {
	return stringOrDefault(s.SecretAccessKeyKey, "AWS_SECRET_ACCESS_KEY")
}

// IsEnabled reports whether OIDC should be configured. Nil defaults to true.
func (s *AuthSpec) IsEnabled() bool {
	return boolDefaultTrue(s.Enabled)
}

// IsEmbedded reports whether Keycloak should be operator-managed. Nil defaults to true.
func (s *AuthSpec) IsEmbedded() bool {
	return boolDefaultTrue(s.Embedded)
}

// NamespaceOrDefault returns the Keycloak namespace.
func (s *KeycloakSpec) NamespaceOrDefault() string {
	return stringOrDefault(s.Namespace, DefaultKeycloakNamespace)
}

// ImageOrDefault returns the Keycloak image.
func (s *KeycloakSpec) ImageOrDefault() string {
	return stringOrDefault(s.Image, DefaultKeycloakImage)
}

// RealmOrDefault returns the Keycloak realm name.
func (s *KeycloakSpec) RealmOrDefault() string {
	return stringOrDefault(s.Realm, DefaultOIDCRealm)
}

// IsEnabled reports whether OpenFGA should be used. Nil defaults to true.
func (s *AuthzSpec) IsEnabled() bool {
	return boolDefaultTrue(s.Enabled)
}

// IsEmbedded reports whether OpenFGA should be operator-managed. Nil defaults to true.
func (s *AuthzSpec) IsEmbedded() bool {
	return boolDefaultTrue(s.Embedded)
}

// NamespaceOrDefault returns the OpenFGA namespace.
func (s *OpenFGASpec) NamespaceOrDefault() string {
	return stringOrDefault(s.Namespace, DefaultOpenFGANamespace)
}

// ImageOrDefault returns the OpenFGA image.
func (s *OpenFGASpec) ImageOrDefault() string {
	return stringOrDefault(s.Image, DefaultOpenFGAImage)
}

// OPAImageOrDefault returns the OPA image.
func (s *OpenFGASpec) OPAImageOrDefault() string {
	return stringOrDefault(s.OPAImage, DefaultOPAImage)
}

// StoreOrDefault returns the OpenFGA store name.
func (s *OpenFGASpec) StoreOrDefault() string {
	return stringOrDefault(s.Store, DefaultOpenFGAStore)
}

// RowFilterStoreOrDefault returns the OpenFGA store name used for row filters.
func (s *OpenFGASpec) RowFilterStoreOrDefault() string {
	return stringOrDefault(s.RowFilterStore, DefaultRowFilterStore)
}

// HasRowFilters reports whether any row filter is configured.
func (s *AuthzSpec) HasRowFilters() bool {
	return s.IsEnabled() && len(s.RowFilters) > 0
}

// HasColumnAccess reports whether any column restriction is configured.
func (s *AuthzSpec) HasColumnAccess() bool {
	return s.IsEnabled() && len(s.ColumnAccess) > 0
}

// HasAccessPolicies reports whether OPA needs the dedicated OpenFGA store
// for row filters or column access.
func (s *AuthzSpec) HasAccessPolicies() bool {
	return s.HasRowFilters() || s.HasColumnAccess()
}

// CatalogOrDefault returns the Trino catalog the filtered table lives in.
func (s *RowFilterSpec) CatalogOrDefault() string {
	return stringOrDefault(s.Catalog, DefaultTrinoCatalog)
}

// IsNumeric reports whether the column is compared against unquoted literals.
func (s *RowFilterSpec) IsNumeric() bool {
	return s.Numeric != nil && *s.Numeric
}

// RelationOrDefault returns the OpenFGA relation guarding a permitted value.
func (s *RowFilterSubjectSpec) RelationOrDefault() string {
	return stringOrDefault(s.Relation, DefaultRowFilterRelation)
}

// CatalogOrDefault returns the Trino catalog the restricted column lives in.
func (s *ColumnAccessSpec) CatalogOrDefault() string {
	return stringOrDefault(s.Catalog, DefaultTrinoCatalog)
}

// MaskOrDefault returns the SQL expression applied when the user has no grant.
func (s *ColumnAccessSpec) MaskOrDefault() string {
	return stringOrDefault(s.Mask, DefaultColumnMask)
}

// RelationOrDefault returns the OpenFGA relation that reveals the column.
func (s *ColumnAccessSubjectSpec) RelationOrDefault() string {
	return stringOrDefault(s.Relation, DefaultRowFilterRelation)
}

// ObjectOrDefault returns the OpenFGA object id for this column.
func (s *ColumnAccessSpec) ObjectOrDefault() string {
	if s.OpenFGA.Object != "" {
		return s.OpenFGA.Object
	}
	return s.CatalogOrDefault() + "." + s.Schema + "." + s.Table + "." + s.Column
}

// AudienceOrDefault returns the expected JWT audience.
func (s *OIDCSpec) AudienceOrDefault() string {
	return stringOrDefault(s.Audience, DefaultOIDCAudience)
}

// ClientIDOrDefault returns the primary OIDC client ID.
func (s *OIDCSpec) ClientIDOrDefault() string {
	return stringOrDefault(s.ClientID, DefaultOIDCClientID)
}

// ScopeOrDefault returns the OAuth2 scope.
func (s *OIDCSpec) ScopeOrDefault() string {
	return stringOrDefault(s.Scope, DefaultOIDCScope)
}

// TokenEndpointOrDefault returns the token URL.
func (s *OIDCSpec) TokenEndpointOrDefault() string {
	if s.TokenEndpoint != "" {
		return s.TokenEndpoint
	}
	return strings.TrimRight(s.Issuer, "/") + "/protocol/openid-connect/token"
}

// ClientSecretKeyOrDefault returns the Secret key for the OIDC client secret.
func (s *OIDCCredentialsSecretRef) ClientSecretKeyOrDefault() string {
	return stringOrDefault(s.ClientSecretKey, "clientSecret")
}

// UsernameKeyOrDefault returns the Secret key for the database user.
func (s *PostgresCredentialsSecretRef) UsernameKeyOrDefault() string {
	return stringOrDefault(s.UsernameKey, "username")
}

// PasswordKeyOrDefault returns the Secret key for the database password.
func (s *PostgresCredentialsSecretRef) PasswordKeyOrDefault() string {
	return stringOrDefault(s.PasswordKey, "password")
}
