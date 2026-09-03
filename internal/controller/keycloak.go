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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

func (r *DataPlatformReconciler) reconcileKeycloak(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform) (oidcConfig, bool, error) {
	spec := dp.Spec.Auth.Keycloak
	ns := spec.NamespaceOrDefault()
	realm := spec.RealmOrDefault()
	dp.Status.KeycloakEndpoint = clusterServiceURL(nameKeycloak, ns, keycloakPort)

	if err := r.ensureNamespace(ctx, dp, ns, componentKeycloak); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionAuthReady, metav1.ConditionFalse, reasonError, err.Error())
		return oidcConfig{}, false, err
	}
	if err := r.ensureKeycloakSecrets(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionAuthReady, metav1.ConditionFalse, reasonError, err.Error())
		return oidcConfig{}, false, err
	}
	realmHash, err := r.applyKeycloakRealm(ctx, dp, ns, spec)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionAuthReady, metav1.ConditionFalse, reasonError, err.Error())
		return oidcConfig{}, false, err
	}
	if err := r.applyKeycloakService(ctx, dp, ns); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionAuthReady, metav1.ConditionFalse, reasonError, err.Error())
		return oidcConfig{}, false, err
	}
	if err := r.applyKeycloakDeployment(ctx, dp, ns, spec, realmHash); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionAuthReady, metav1.ConditionFalse, reasonError, err.Error())
		return oidcConfig{}, false, err
	}

	cfg, err := r.embeddedOIDCConfig(ctx, dp, ns, realm)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionAuthReady, metav1.ConditionFalse, reasonError, err.Error())
		return oidcConfig{}, false, err
	}

	ready, err := r.deploymentReady(ctx, ns, nameKeycloak)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionAuthReady, metav1.ConditionFalse, reasonError, err.Error())
		return cfg, false, err
	}
	if !ready {
		setCondition(dp, dataplatformv1alpha1.ConditionAuthReady, metav1.ConditionFalse, reasonNotReady, "Keycloak Deployment is not ready")
		return cfg, false, nil
	}
	setCondition(dp, dataplatformv1alpha1.ConditionAuthReady, metav1.ConditionTrue, reasonReady, "Keycloak is ready")
	return cfg, true, nil
}

func (r *DataPlatformReconciler) embeddedOIDCConfig(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, ns, realm string) (oidcConfig, error) {
	issuer := oidcIssuer(dp.Status.KeycloakEndpoint, realm)
	publicIssuer := issuer
	if dp.Spec.Auth.Keycloak.PublicURL != "" {
		publicIssuer = oidcIssuer(dp.Spec.Auth.Keycloak.PublicURL, realm)
	}

	operatorID, err := r.getSecretData(ctx, secretOIDC, ns, keyOIDCOperatorClientID)
	if err != nil {
		return oidcConfig{}, err
	}
	operatorSecret, err := r.getSecretData(ctx, secretOIDC, ns, keyOIDCOperatorSecret)
	if err != nil {
		return oidcConfig{}, err
	}
	trinoID, err := r.getSecretData(ctx, secretOIDC, ns, keyOIDCTrinoClientID)
	if err != nil {
		return oidcConfig{}, err
	}
	trinoSecret, err := r.getSecretData(ctx, secretOIDC, ns, keyOIDCTrinoClientSecret)
	if err != nil {
		return oidcConfig{}, err
	}
	uiID, err := r.getSecretData(ctx, secretOIDC, ns, keyOIDCClientID)
	if err != nil {
		return oidcConfig{}, err
	}
	opaID, err := r.getSecretData(ctx, secretOIDC, ns, keyOIDCOpaClientID)
	if err != nil {
		return oidcConfig{}, err
	}
	opaSecret, err := r.getSecretData(ctx, secretOIDC, ns, keyOIDCOpaClientSecret)
	if err != nil {
		return oidcConfig{}, err
	}

	return oidcConfig{
		enabled:          true,
		issuer:           issuer,
		publicIssuer:     publicIssuer,
		audience:         dataplatformv1alpha1.DefaultOIDCAudience,
		scope:            dataplatformv1alpha1.DefaultOIDCScope,
		uiClientID:       uiID,
		trinoClientID:    trinoID,
		trinoSecret:      trinoSecret,
		opaClientID:      opaID,
		opaSecret:        opaSecret,
		operatorClientID: operatorID,
		operatorSecret:   operatorSecret,
		adminSubject:     dataplatformv1alpha1.DefaultOIDCAdminUserID,
		adminName:        dataplatformv1alpha1.DefaultOIDCAdminUser,
		adminEmail:       keycloakAdminEmail,
		opaSubject:       keycloakUUID("service-account", opaID),
		tokenURL:         oidcTokenURL(issuer),
		proxyNamespace:   ns,
		proxyService:     nameKeycloak,
		proxyPort:        keycloakPort,
		proxyTokenPath:   oidcTokenPath(realm),
	}, nil
}

// ensureKeycloakRealmKey generates the realm's RS256 signing key once and keeps
// it in a Secret. Keycloak holds realm keys in its own database, which is an
// emptyDir here, so without importing a stable key every pod recreate would
// resign with a fresh key and reject tokens issued before it.
func (r *DataPlatformReconciler) ensureKeycloakRealmKey(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
) error {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretKeycloakRealmKey, Namespace: ns}, secret)
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return err
	}
	privateKey, certificate, err := generateRealmSigningKey(dp.Spec.Auth.Keycloak.RealmOrDefault())
	if err != nil {
		return err
	}
	return r.ensureGeneratedSecret(ctx, dp, secretKeycloakRealmKey, ns, componentKeycloak, map[string][]byte{
		keyRealmPrivateKey:  []byte(privateKey),
		keyRealmCertificate: []byte(certificate),
	})
}

// generateRealmSigningKey returns a PKCS#8 key and self-signed certificate,
// both base64-encoded DER as Keycloak's imported-key provider expects.
func generateRealmSigningKey(realm string) (string, string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: realm},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:         true,
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(der), base64.StdEncoding.EncodeToString(certificate), nil
}

func (r *DataPlatformReconciler) ensureKeycloakSecrets(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, ns string) error {
	if err := r.ensureKeycloakRealmKey(ctx, dp, ns); err != nil {
		return err
	}
	admin := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretKeycloakAdmin, Namespace: ns}, admin)
	if errors.IsNotFound(err) {
		password, genErr := randomHex(16)
		if genErr != nil {
			return genErr
		}
		if err := r.ensureGeneratedSecret(ctx, dp, secretKeycloakAdmin, ns, componentKeycloak, map[string][]byte{
			keyKeycloakAdminUser:     []byte(dataplatformv1alpha1.DefaultOIDCAdminUser),
			keyKeycloakAdminPassword: []byte(password),
		}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	oidc := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{Name: secretOIDC, Namespace: ns}, oidc)
	if errors.IsNotFound(err) {
		trinoSecret, genErr := randomHex(16)
		if genErr != nil {
			return genErr
		}
		operatorSecret, genErr := randomHex(16)
		if genErr != nil {
			return genErr
		}
		opaSecret, genErr := randomHex(16)
		if genErr != nil {
			return genErr
		}
		return r.ensureGeneratedSecret(ctx, dp, secretOIDC, ns, componentKeycloak, map[string][]byte{
			keyOIDCClientID:          []byte(dataplatformv1alpha1.DefaultOIDCClientID),
			keyOIDCTrinoClientID:     []byte(dataplatformv1alpha1.DefaultOIDCTrinoClientID),
			keyOIDCTrinoClientSecret: []byte(trinoSecret),
			keyOIDCOperatorClientID:  []byte(dataplatformv1alpha1.DefaultOIDCOperatorClient),
			keyOIDCOperatorSecret:    []byte(operatorSecret),
			keyOIDCOpaClientID:       []byte(dataplatformv1alpha1.DefaultOIDCOpaClientID),
			keyOIDCOpaClientSecret:   []byte(opaSecret),
		})
	}
	if err != nil {
		return err
	}
	return r.ensureOIDCSecretKeys(ctx, oidc)
}

func (r *DataPlatformReconciler) ensureOIDCSecretKeys(ctx context.Context, oidc *corev1.Secret) error {
	if oidc.Data == nil {
		oidc.Data = map[string][]byte{}
	}
	changed := false
	if _, ok := oidc.Data[keyOIDCOpaClientID]; !ok {
		oidc.Data[keyOIDCOpaClientID] = []byte(dataplatformv1alpha1.DefaultOIDCOpaClientID)
		changed = true
	}
	if _, ok := oidc.Data[keyOIDCOpaClientSecret]; !ok {
		secret, err := randomHex(16)
		if err != nil {
			return err
		}
		oidc.Data[keyOIDCOpaClientSecret] = []byte(secret)
		changed = true
	}
	if !changed {
		return nil
	}
	return r.Update(ctx, oidc)
}

func (r *DataPlatformReconciler) applyKeycloakRealm(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
	spec dataplatformv1alpha1.KeycloakSpec,
) (string, error) {
	adminPass, err := r.getSecretData(ctx, secretKeycloakAdmin, ns, keyKeycloakAdminPassword)
	if err != nil {
		return "", err
	}
	trinoSecret, err := r.getSecretData(ctx, secretOIDC, ns, keyOIDCTrinoClientSecret)
	if err != nil {
		return "", err
	}
	operatorSecret, err := r.getSecretData(ctx, secretOIDC, ns, keyOIDCOperatorSecret)
	if err != nil {
		return "", err
	}
	opaSecret, err := r.getSecretData(ctx, secretOIDC, ns, keyOIDCOpaClientSecret)
	if err != nil {
		return "", err
	}
	signingKey, err := r.getSecretData(ctx, secretKeycloakRealmKey, ns, keyRealmPrivateKey)
	if err != nil {
		return "", err
	}
	signingCert, err := r.getSecretData(ctx, secretKeycloakRealmKey, ns, keyRealmCertificate)
	if err != nil {
		return "", err
	}
	raw, err := keycloakRealmJSON(realmOptions{
		spec:                spec,
		adminPassword:       adminPass,
		trinoSecret:         trinoSecret,
		operatorSecret:      operatorSecret,
		opaSecret:           opaSecret,
		signingKey:          signingKey,
		signingCertificate:  signingCert,
		lakekeeperNamespace: dp.Spec.Lakekeeper.NamespaceOrDefault(),
		lakekeeperPublicURL: dp.Spec.Lakekeeper.PublicURL,
		trinoPublicURL:      dp.Spec.Trino.PublicURL,
	})
	if err != nil {
		return "", err
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: configMapKeycloakRealm, Namespace: ns}}
	labels := labelsFor(dp, componentKeycloak)
	if err := r.apply(ctx, dp, cm, func() error {
		ensureLabels(cm, labels)
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[keyRealmJSON] = raw
		return nil
	}); err != nil {
		return "", err
	}
	return hashData(raw), nil
}

func (r *DataPlatformReconciler) applyKeycloakService(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, ns string) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: nameKeycloak, Namespace: ns}}
	labels := labelsFor(dp, componentKeycloak)
	return r.apply(ctx, dp, svc, func() error {
		ensureLabels(svc, labels)
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       portNameHTTP,
			Port:       keycloakPort,
			TargetPort: intstr.FromInt32(keycloakPort),
		}}
		return nil
	})
}

func (r *DataPlatformReconciler) applyKeycloakDeployment(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
	spec dataplatformv1alpha1.KeycloakSpec,
	realmHash string,
) error {
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: nameKeycloak, Namespace: ns}}
	labels := labelsFor(dp, componentKeycloak)
	return r.apply(ctx, dp, deploy, func() error {
		ensureLabels(deploy, labels)
		if deploy.CreationTimestamp.IsZero() {
			deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		}
		deploy.Spec.Replicas = ptr.To(int32(1))
		if deploy.Spec.Template.Annotations == nil {
			deploy.Spec.Template.Annotations = map[string]string{}
		}
		deploy.Spec.Template.Annotations[annotationConfigHash] = realmHash
		deploy.Spec.Template.Labels = labels
		deploy.Spec.Template.Spec.SecurityContext = restrictedPodSecurity(uidKeycloak, gidKeycloak)
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  nameKeycloak,
			Image: spec.ImageOrDefault(),
			Args:  []string{"start-dev", "--import-realm", "--http-port=" + fmt.Sprintf("%d", keycloakPort)},
			Ports: []corev1.ContainerPort{{Name: portNameHTTP, ContainerPort: keycloakPort}},
			Env: append([]corev1.EnvVar{
				{
					Name: "KC_BOOTSTRAP_ADMIN_USERNAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: secretKeycloakAdmin},
							Key:                  keyKeycloakAdminUser,
						},
					},
				},
				{
					Name: "KC_BOOTSTRAP_ADMIN_PASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: secretKeycloakAdmin},
							Key:                  keyKeycloakAdminPassword,
						},
					},
				},
			}, keycloakHostnameEnv(ns, spec)...),
			VolumeMounts: []corev1.VolumeMount{
				{Name: volumeData, MountPath: "/opt/keycloak/data"},
				{Name: volumeRealm, MountPath: "/opt/keycloak/data/import"},
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/realms/" + spec.RealmOrDefault(),
						Port: intstr.FromInt32(keycloakPort),
					},
				},
				InitialDelaySeconds: 40,
				PeriodSeconds:       10,
			},
			Resources:       spec.Resources,
			SecurityContext: restrictedContainerSecurity(uidKeycloak, gidKeycloak),
		}}
		deploy.Spec.Template.Spec.Volumes = []corev1.Volume{
			{Name: volumeData, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			{
				Name: volumeRealm,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: configMapKeycloakRealm},
					},
				},
			},
		}
		return nil
	})
}

// keycloakHostnameEnv configures Keycloak 26 hostname v2 so browsers stay on the
// public ingress URL. In-cluster callers still use cluster DNS; when publicURL is
// set, hostname-backchannel-dynamic lets those requests keep internal URLs.
func keycloakHostnameEnv(ns string, spec dataplatformv1alpha1.KeycloakSpec) []corev1.EnvVar {
	hostname := strings.TrimRight(spec.PublicURL, "/")
	if hostname == "" {
		hostname = fmt.Sprintf("http://%s.%s.svc:%d", nameKeycloak, ns, keycloakPort)
	}
	env := []corev1.EnvVar{
		{Name: "KC_HTTP_ENABLED", Value: "true"},
		{Name: "KC_HOSTNAME_STRICT", Value: "false"},
		{Name: "KC_HOSTNAME", Value: hostname},
		{Name: "KC_PROXY_HEADERS", Value: "xforwarded"},
		{Name: "KC_HEALTH_ENABLED", Value: "true"},
	}
	if spec.PublicURL != "" {
		env = append(env, corev1.EnvVar{Name: "KC_HOSTNAME_BACKCHANNEL_DYNAMIC", Value: "true"})
	}
	return env
}

// realmOptions carries everything the realm import needs.
type realmOptions struct {
	spec                dataplatformv1alpha1.KeycloakSpec
	adminPassword       string
	trinoSecret         string
	operatorSecret      string
	opaSecret           string
	signingKey          string
	signingCertificate  string
	lakekeeperNamespace string
	lakekeeperPublicURL string
	trinoPublicURL      string
}

func keycloakRealmJSON(opts realmOptions) (string, error) {
	spec := opts.spec
	adminPass := opts.adminPassword
	trinoSecret := opts.trinoSecret
	operatorSecret := opts.operatorSecret
	opaSecret := opts.opaSecret
	lakekeeperPublicURL := opts.lakekeeperPublicURL
	trinoPublicURL := opts.trinoPublicURL
	realm := spec.RealmOrDefault()
	lkCallback := clusterServiceURL(nameLakekeeper, opts.lakekeeperNamespace, lakekeeperPort) + "/ui/callback"
	redirects := []string{
		lkCallback,
		"http://localhost:8181/ui/callback",
		"http://127.0.0.1:8181/ui/callback",
		"*",
	}
	if u := strings.TrimRight(lakekeeperPublicURL, "/"); u != "" {
		redirects = append(redirects, u+"/ui/callback", u+"/*")
	}

	trinoRedirects := []string{}
	if u := strings.TrimRight(trinoPublicURL, "/"); u != "" {
		trinoRedirects = append(trinoRedirects, u+"/oauth2/callback")
	}

	doc := map[string]any{
		"realm":               realm,
		"enabled":             true,
		"sslRequired":         "NONE",
		"accessTokenLifespan": 43200,
		"roles": map[string]any{
			"realm": []map[string]any{
				{"name": "default-roles-" + realm, "composite": true},
			},
		},
		// Keycloak 24+ only puts `sub` on access tokens via the `basic` scope.
		// Importing a custom clientScopes list replaces the built-in scopes, so
		// we must ship basic/profile/email ourselves.
		"clientScopes":               oidcClientScopes(),
		"defaultDefaultClientScopes": defaultOIDCClientScopes(),
		"components":                 realmKeyComponents(opts.signingKey, opts.signingCertificate),
		"clients": []map[string]any{
			{
				"clientId":                  dataplatformv1alpha1.DefaultOIDCClientID,
				"name":                      "LakeKeeper",
				"enabled":                   true,
				"protocol":                  "openid-connect",
				"publicClient":              true,
				"standardFlowEnabled":       true,
				"directAccessGrantsEnabled": true,
				"serviceAccountsEnabled":    false,
				"redirectUris":              redirects,
				"webOrigins":                []string{"+"},
				"defaultClientScopes":       defaultOIDCClientScopes(),
				"optionalClientScopes":      []string{},
				"attributes": map[string]string{
					"oauth2.device.authorization.grant.enabled": "true",
					"pkce.code.challenge.method":                "S256",
				},
			},
			confidentialClient(dataplatformv1alpha1.DefaultOIDCTrinoClientID, "Trino", trinoSecret, trinoRedirects),
			confidentialClient(dataplatformv1alpha1.DefaultOIDCOpaClientID, "OPA", opaSecret, nil),
			confidentialClient(dataplatformv1alpha1.DefaultOIDCOperatorClient, "Data Platform Operator", operatorSecret, nil),
		},
		"users": []map[string]any{
			{
				"id":            dataplatformv1alpha1.DefaultOIDCAdminUserID,
				"username":      dataplatformv1alpha1.DefaultOIDCAdminUser,
				"enabled":       true,
				"emailVerified": true,
				"email":         keycloakAdminEmail,
				"firstName":     "Admin",
				"lastName":      "Local",
				"credentials": []map[string]any{{
					"type":      "password",
					"value":     adminPass,
					"temporary": false,
				}},
			},
			serviceAccountUser(dataplatformv1alpha1.DefaultOIDCTrinoClientID),
			serviceAccountUser(dataplatformv1alpha1.DefaultOIDCOpaClientID),
			serviceAccountUser(dataplatformv1alpha1.DefaultOIDCOperatorClient),
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// keycloakUUID derives a stable id for an imported realm object. Keycloak
// assigns random ids on import and stores the realm on an emptyDir, so without
// pinning them every pod restart would issue new subjects for the service
// accounts and invalidate the LakeKeeper grants held against the old ones.
func keycloakUUID(kind, name string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://dataplatform.opsarray.io/keycloak/"+kind+"/"+name)).String()
}

// serviceAccountUser pins the identity Keycloak would otherwise generate for a
// confidential client's service account.
func serviceAccountUser(clientID string) map[string]any {
	return map[string]any{
		"id":                     keycloakUUID("service-account", clientID),
		"username":               "service-account-" + clientID,
		"enabled":                true,
		"serviceAccountClientId": clientID,
	}
}

// realmKeyComponents imports the operator's RS256 key as the realm's highest
// priority signing key. Supplying any key provider suppresses the ones Keycloak
// would generate, so the HMAC and AES providers other flows rely on are listed
// too; only the RS256 key that signs tokens has to survive a restart.
func realmKeyComponents(signingKey, certificate string) map[string]any {
	return map[string]any{
		"org.keycloak.keys.KeyProvider": []map[string]any{
			{
				"name":       "operator-rsa",
				"providerId": "rsa",
				"config": map[string][]string{
					"privateKey":  {signingKey},
					"certificate": {certificate},
					"algorithm":   {"RS256"},
					"priority":    {"200"},
					"active":      {"true"},
					"enabled":     {"true"},
				},
			},
			{
				"name":       "hmac-generated",
				"providerId": "hmac-generated",
				"config": map[string][]string{
					"algorithm": {"HS512"},
					"priority":  {"100"},
				},
			},
			{
				"name":       "aes-generated",
				"providerId": "aes-generated",
				"config":     map[string][]string{"priority": {"100"}},
			},
			{
				"name":       "rsa-enc-generated",
				"providerId": "rsa-enc-generated",
				"config": map[string][]string{
					"algorithm": {"RSA-OAEP"},
					"priority":  {"100"},
				},
			},
		},
	}
}

func confidentialClient(id, name, secret string, redirectURIs []string) map[string]any {
	mappers := []map[string]any{audienceMapper(id)}
	client := map[string]any{
		"id":                        keycloakUUID("client", id),
		"clientId":                  id,
		"name":                      name,
		"enabled":                   true,
		"protocol":                  "openid-connect",
		"publicClient":              false,
		"secret":                    secret,
		"serviceAccountsEnabled":    true,
		"standardFlowEnabled":       false,
		"directAccessGrantsEnabled": false,
		"defaultClientScopes":       defaultOIDCClientScopes(),
		"optionalClientScopes":      []string{},
		"protocolMappers":           mappers,
	}
	if len(redirectURIs) > 0 {
		client["standardFlowEnabled"] = true
		client["redirectUris"] = redirectURIs
		client["webOrigins"] = []string{"+"}
		mappers = append(mappers, map[string]any{
			"name":           "username",
			"protocol":       "openid-connect",
			"protocolMapper": "oidc-usermodel-property-mapper",
			"config": map[string]string{
				"user.attribute":       "username",
				"claim.name":           "preferred_username",
				"jsonType.label":       "String",
				"id.token.claim":       "true",
				"access.token.claim":   "true",
				"userinfo.token.claim": "true",
			},
		})
		client["protocolMappers"] = mappers
	}
	return client
}

func audienceMapper(clientID string) map[string]any {
	return map[string]any{
		"name":           "audience-" + clientID,
		"protocol":       "openid-connect",
		"protocolMapper": "oidc-audience-mapper",
		"config": map[string]string{
			"included.client.audience":  clientID,
			"id.token.claim":            "false",
			"access.token.claim":        "true",
			"introspection.token.claim": "true",
		},
	}
}

func defaultOIDCClientScopes() []string {
	return []string{"basic", "profile", "email", dataplatformv1alpha1.DefaultOIDCScope}
}

func oidcClientScopes() []map[string]any {
	return []map[string]any{
		{
			"name":        "basic",
			"description": "OpenID Connect scope for add all basic claims to the token",
			"protocol":    "openid-connect",
			"attributes": map[string]string{
				"include.in.token.scope":    "false",
				"display.on.consent.screen": "false",
			},
			"protocolMappers": []map[string]any{
				{
					"name":           "sub",
					"protocol":       "openid-connect",
					"protocolMapper": "oidc-sub-mapper",
					"config": map[string]string{
						"access.token.claim":        "true",
						"introspection.token.claim": "true",
					},
				},
			},
		},
		{
			"name":        "profile",
			"description": "OpenID Connect built-in scope: profile",
			"protocol":    "openid-connect",
			"attributes": map[string]string{
				"include.in.token.scope":    "true",
				"display.on.consent.screen": "false",
			},
			"protocolMappers": []map[string]any{
				userPropertyMapper("username", "preferred_username"),
				userPropertyMapper("firstName", "given_name"),
				userPropertyMapper("lastName", "family_name"),
			},
		},
		{
			"name":        "email",
			"description": "OpenID Connect built-in scope: email",
			"protocol":    "openid-connect",
			"attributes": map[string]string{
				"include.in.token.scope":    "true",
				"display.on.consent.screen": "false",
			},
			"protocolMappers": []map[string]any{
				userPropertyMapper("email", "email"),
			},
		},
		{
			"name":        dataplatformv1alpha1.DefaultOIDCScope,
			"description": "Audience for LakeKeeper",
			"protocol":    "openid-connect",
			"attributes": map[string]string{
				"include.in.token.scope":    "true",
				"display.on.consent.screen": "false",
			},
			"protocolMappers": []map[string]any{
				{
					"name":           "audience-lakekeeper",
					"protocol":       "openid-connect",
					"protocolMapper": "oidc-audience-mapper",
					"config": map[string]string{
						"included.client.audience":  dataplatformv1alpha1.DefaultOIDCAudience,
						"id.token.claim":            "false",
						"access.token.claim":        "true",
						"introspection.token.claim": "true",
					},
				},
			},
		},
	}
}

func userPropertyMapper(property, claim string) map[string]any {
	return map[string]any{
		"name":           claim,
		"protocol":       "openid-connect",
		"protocolMapper": "oidc-usermodel-property-mapper",
		"config": map[string]string{
			"user.attribute":       property,
			"claim.name":           claim,
			"jsonType.label":       "String",
			"id.token.claim":       "true",
			"access.token.claim":   "true",
			"userinfo.token.claim": "true",
		},
	}
}
