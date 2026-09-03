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
	"encoding/json"
	"io/fs"
	"path"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

func (r *DataPlatformReconciler) reconcileOPA(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, oidc oidcConfig, fga openfgaConfig) (bool, error) {
	if !fga.enabled || !oidc.enabled || !dp.Spec.Trino.IsEnabled() {
		return true, nil
	}
	ns := dp.Spec.Authz.OpenFGA.NamespaceOrDefault()
	if err := r.applyOPASecret(ctx, dp, ns, oidc); err != nil {
		return false, err
	}
	if err := r.applyOPAPolicies(ctx, dp, ns); err != nil {
		return false, err
	}
	if err := r.applyOPAService(ctx, dp, ns); err != nil {
		return false, err
	}
	if err := r.applyOPADeployment(ctx, dp, ns, oidc, fga); err != nil {
		return false, err
	}
	return r.deploymentReady(ctx, ns, nameOPA)
}

func (r *DataPlatformReconciler) applyOPASecret(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, ns string, oidc oidcConfig) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretOIDC, Namespace: ns}}
	labels := labelsFor(dp, componentOPA)
	return r.apply(ctx, dp, secret, func() error {
		ensureLabels(secret, labels)
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = map[string][]byte{
			keyOIDCOpaClientID:     []byte(oidc.opaClientID),
			keyOIDCOpaClientSecret: []byte(oidc.opaSecret),
		}
		return nil
	})
}

func (r *DataPlatformReconciler) applyOPAPolicies(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, ns string) error {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: configMapOPAPolicies, Namespace: ns}}
	labels := labelsFor(dp, componentOPA)
	return r.apply(ctx, dp, cm, func() error {
		ensureLabels(cm, labels)
		data := map[string]string{}
		for _, dir := range opaPolicyDirs {
			err := fs.WalkDir(opaPolicyFS, dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || !strings.HasSuffix(p, ".rego") {
					return nil
				}
				raw, err := opaPolicyFS.ReadFile(p)
				if err != nil {
					return err
				}
				data[path.Base(p)] = string(raw)
				return nil
			})
			if err != nil {
				return err
			}
		}
		cm.Data = data
		return nil
	})
}

// opaAccessEnv configures the operator's row-filter and column-access policies.
// It is empty when neither is configured, which leaves those rules with
// nothing to match.
//
// The policies travel as environment variables rather than ConfigMap keys
// because OPA does not run with --watch: a ConfigMap edit would sit unread
// until the pod restarted, whereas changing the pod's env rolls it.
func opaAccessEnv(dp *dataplatformv1alpha1.DataPlatform, fga openfgaConfig) []corev1.EnvVar {
	if !dp.Spec.Authz.HasAccessPolicies() {
		return nil
	}
	env := []corev1.EnvVar{
		{Name: "OPENFGA_URL", Value: fga.httpEndpoint},
		{Name: "OPENFGA_STORE_ID", Value: fga.rowFilterStoreID},
		{
			Name: "OPENFGA_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretOpenFGA},
					Key:                  keyOpenFGAAPIKey,
				},
			},
		},
	}
	if dp.Spec.Authz.HasRowFilters() {
		env = append(env, corev1.EnvVar{Name: "TRINO_ROW_FILTERS", Value: rowFiltersJSON(dp.Spec.Authz.RowFilters)})
	}
	if dp.Spec.Authz.HasColumnAccess() {
		env = append(env, corev1.EnvVar{Name: "TRINO_COLUMN_ACCESS", Value: columnAccessJSON(dp.Spec.Authz.ColumnAccess)})
	}
	return env
}

// rowFiltersJSON renders the filters in the shape rowfilters-configuration.rego
// expects, with every default resolved so the policy never has to guess.
func rowFiltersJSON(filters []dataplatformv1alpha1.RowFilterSpec) string {
	entries := make([]map[string]any, 0, len(filters))
	for i := range filters {
		entries = append(entries, map[string]any{
			"catalog":  filters[i].CatalogOrDefault(),
			"schema":   filters[i].Schema,
			"table":    filters[i].Table,
			"column":   filters[i].Column,
			"type":     filters[i].OpenFGA.Type,
			"relation": filters[i].OpenFGA.RelationOrDefault(),
			"numeric":  filters[i].IsNumeric(),
		})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		// The map holds only strings and bools, so this cannot fail.
		return "[]"
	}
	return string(raw)
}

// columnAccessJSON renders the restrictions in the shape
// rowfilters-configuration.rego expects, with every default resolved.
func columnAccessJSON(access []dataplatformv1alpha1.ColumnAccessSpec) string {
	entries := make([]map[string]any, 0, len(access))
	for i := range access {
		entries = append(entries, map[string]any{
			"catalog":  access[i].CatalogOrDefault(),
			"schema":   access[i].Schema,
			"table":    access[i].Table,
			"column":   access[i].Column,
			"mask":     access[i].MaskOrDefault(),
			"type":     access[i].OpenFGA.Type,
			"relation": access[i].OpenFGA.RelationOrDefault(),
			"object":   access[i].ObjectOrDefault(),
		})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func (r *DataPlatformReconciler) applyOPAService(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, ns string) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: nameOPA, Namespace: ns}}
	labels := labelsFor(dp, componentOPA)
	return r.apply(ctx, dp, svc, func() error {
		ensureLabels(svc, labels)
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       portNameHTTP,
			Port:       opaPort,
			TargetPort: intstr.FromInt32(opaPort),
		}}
		return nil
	})
}

func (r *DataPlatformReconciler) applyOPADeployment(
	ctx context.Context,
	dp *dataplatformv1alpha1.DataPlatform,
	ns string,
	oidc oidcConfig,
	fga openfgaConfig,
) error {
	lkURL := clusterServiceURL(nameLakekeeper, dp.Spec.Lakekeeper.NamespaceOrDefault(), lakekeeperPort)
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: nameOPA, Namespace: ns}}
	labels := labelsFor(dp, componentOPA)
	return r.apply(ctx, dp, deploy, func() error {
		ensureLabels(deploy, labels)
		if deploy.CreationTimestamp.IsZero() {
			deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		}
		deploy.Spec.Replicas = ptr.To(int32(1))
		deploy.Spec.Template.Labels = labels
		deploy.Spec.Template.Spec.SecurityContext = restrictedPodSecurity(uidOPA, gidOPA)
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  nameOPA,
			Image: dp.Spec.Authz.OpenFGA.OPAImageOrDefault(),
			// A ConfigMap volume keeps every key under symlinked ..data and
			// ..<timestamp> directories, so a recursive load would parse each
			// policy three times. Skip dot-prefixed entries.
			Args: []string{
				"run", "--server",
				"--addr", "0.0.0.0:8181",
				"--ignore", ".*",
				"/policies",
			},
			Env: append([]corev1.EnvVar{
				{Name: "LAKEKEEPER_URL", Value: lkURL},
				{Name: "LAKEKEEPER_TOKEN_ENDPOINT", Value: oidc.tokenURL},
				{Name: "LAKEKEEPER_CLIENT_ID", Value: oidc.opaClientID},
				{
					Name: "LAKEKEEPER_CLIENT_SECRET",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: secretOIDC},
							Key:                  keyOIDCOpaClientSecret,
						},
					},
				},
				{Name: "LAKEKEEPER_SCOPE", Value: oidc.scope},
				{Name: "TRINO_LAKEKEEPER_CATALOG_NAME", Value: nameLakekeeper},
				{Name: "LAKEKEEPER_LAKEKEEPER_WAREHOUSE", Value: dp.Spec.Lakekeeper.Warehouse.NameOrDefault()},
				{Name: "TRINO_ALLOW_UNMANAGED_CATALOGS", Value: "true"},
			}, opaAccessEnv(dp, fga)...),
			Ports: []corev1.ContainerPort{{Name: portNameHTTP, ContainerPort: opaPort}},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      volumeConfig,
				MountPath: "/policies",
				ReadOnly:  true,
			}},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/health",
						Port: intstr.FromInt32(opaPort),
					},
				},
				PeriodSeconds: 5,
			},
			SecurityContext: restrictedContainerSecurity(uidOPA, gidOPA),
		}}
		deploy.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name: volumeConfig,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapOPAPolicies},
				},
			},
		}}
		return nil
	})
}
