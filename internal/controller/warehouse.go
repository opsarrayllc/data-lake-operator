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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

func (r *DataPlatformReconciler) reconcileWarehouse(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, store objectStore, oidc oidcConfig) error {
	log := logf.FromContext(ctx)
	if r.Catalog == nil {
		setCondition(dp, dataplatformv1alpha1.ConditionWarehouseReady, metav1.ConditionFalse, reasonMissing, "Catalog client is not configured")
		return nil
	}

	token, err := r.oidcAccessToken(ctx, oidc)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionWarehouseReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	if oidc.enabled && token == "" {
		err := fmt.Errorf("OIDC token is empty")
		setCondition(dp, dataplatformv1alpha1.ConditionWarehouseReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}

	ns := dp.Spec.Lakekeeper.NamespaceOrDefault()
	if err := r.Catalog.Bootstrap(ctx, ns, nameLakekeeper, lakekeeperPort, token); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionWarehouseReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	if principals := catalogPrincipals(dp, oidc); len(principals) > 0 {
		if err := r.Catalog.EnsureGrants(ctx, ns, nameLakekeeper, lakekeeperPort, token, principals); err != nil {
			setCondition(dp, dataplatformv1alpha1.ConditionWarehouseReady, metav1.ConditionFalse, reasonError, err.Error())
			return err
		}
		log.Info("Ensured LakeKeeper grants", "principals", len(principals))
	}
	req := WarehouseRequest{
		Name:            dp.Spec.Lakekeeper.Warehouse.NameOrDefault(),
		Bucket:          store.Bucket,
		Region:          store.Region,
		KeyPrefix:       dp.Spec.Lakekeeper.Warehouse.KeyPrefix,
		Endpoint:        store.Endpoint,
		PathStyleAccess: store.PathStyleAccess,
		Flavor:          store.Flavor,
		STSEnabled:      store.STSEnabled,
		STSRoleARN:      store.STSRoleARN,
		AccessKeyID:     store.AccessKeyID,
		SecretAccessKey: store.SecretAccessKey,
	}
	if err := r.Catalog.EnsureWarehouse(ctx, ns, nameLakekeeper, lakekeeperPort, token, req); err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionWarehouseReady, metav1.ConditionFalse, reasonError, err.Error())
		return err
	}
	log.Info("Ensured LakeKeeper warehouse", "name", req.Name)
	setCondition(dp, dataplatformv1alpha1.ConditionWarehouseReady, metav1.ConditionTrue, reasonReady, "Warehouse "+req.Name+" is ready")
	return nil
}

// catalogPrincipals lists the identities that need grants once the catalog is
// bootstrapped. Empty for external issuers, where the operator does not own the
// user list and cannot predict subjects.
func catalogPrincipals(dp *dataplatformv1alpha1.DataPlatform, oidc oidcConfig) []CatalogPrincipal {
	if oidc.adminSubject == "" {
		return nil
	}
	principals := []CatalogPrincipal{{
		Subject:         oidcSubjectPrefix + oidc.adminSubject,
		Name:            oidc.adminName,
		Email:           oidc.adminEmail,
		Type:            "human",
		ServerRelation:  "admin",
		ProjectRelation: "project_admin",
	}}
	// The OPA bridge asks LakeKeeper whether other users may act, which is a
	// privileged read it cannot perform without security_admin.
	if dp.Spec.Authz.IsEnabled() && oidc.opaSubject != "" {
		principals = append(principals, CatalogPrincipal{
			Subject:         oidcSubjectPrefix + oidc.opaSubject,
			Name:            oidc.opaClientID,
			Type:            "application",
			ProjectRelation: "security_admin",
		})
	}
	return principals
}
