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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

const requeueWhileProgressing = 15 * time.Second

// DataPlatformReconciler reconciles a DataPlatform object.
type DataPlatformReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Catalog CatalogClient
}

// +kubebuilder:rbac:groups=dataplatform.opsarray.io,resources=dataplatforms,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dataplatform.opsarray.io,resources=dataplatforms/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dataplatform.opsarray.io,resources=dataplatforms/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=services;configmaps;secrets;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

func (r *DataPlatformReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	dp := &dataplatformv1alpha1.DataPlatform{}
	if err := r.Get(ctx, req.NamespacedName, dp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	result, err := r.reconcile(ctx, dp)
	if statusErr := r.patchStatus(ctx, dp); statusErr != nil {
		log.Error(statusErr, "Failed to update DataPlatform status")
		return ctrl.Result{}, statusErr
	}
	return result, err
}

func (r *DataPlatformReconciler) reconcile(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Reconciling DataPlatform")

	progressing, store, oidc, err := r.reconcileLakekeeperStack(ctx, dp)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionReady, metav1.ConditionFalse, reasonError, err.Error())
		return ctrl.Result{}, err
	}

	trinoProgressing, err := r.reconcileTrinoStack(ctx, dp, store, oidc)
	if err != nil {
		setCondition(dp, dataplatformv1alpha1.ConditionReady, metav1.ConditionFalse, reasonError, err.Error())
		return ctrl.Result{}, err
	}
	progressing = progressing || trinoProgressing

	if progressing {
		setCondition(dp, dataplatformv1alpha1.ConditionReady, metav1.ConditionFalse, reasonReconciling, "Waiting for enabled components to become ready")
		return ctrl.Result{RequeueAfter: requeueWhileProgressing}, nil
	}

	setCondition(dp, dataplatformv1alpha1.ConditionReady, metav1.ConditionTrue, reasonReady, "All enabled components are ready")
	return ctrl.Result{}, nil
}

func (r *DataPlatformReconciler) reconcileLakekeeperStack(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform) (bool, objectStore, oidcConfig, error) {
	if !dp.Spec.Lakekeeper.IsEnabled() {
		setCondition(dp, dataplatformv1alpha1.ConditionMinioReady, metav1.ConditionTrue, reasonDisabled, "LakeKeeper is disabled")
		setCondition(dp, dataplatformv1alpha1.ConditionPostgresReady, metav1.ConditionTrue, reasonDisabled, "LakeKeeper is disabled")
		setCondition(dp, dataplatformv1alpha1.ConditionLakekeeperReady, metav1.ConditionTrue, reasonDisabled, "LakeKeeper is disabled")
		setCondition(dp, dataplatformv1alpha1.ConditionWarehouseReady, metav1.ConditionTrue, reasonDisabled, "LakeKeeper is disabled")
		if !dp.Spec.Auth.IsEnabled() {
			setCondition(dp, dataplatformv1alpha1.ConditionAuthReady, metav1.ConditionTrue, reasonDisabled, "LakeKeeper is disabled")
		}
		return false, objectStore{}, oidcConfig{}, nil
	}

	ns := dp.Spec.Lakekeeper.NamespaceOrDefault()
	if err := r.ensureNamespace(ctx, dp, ns, componentLakekeeper); err != nil {
		return false, objectStore{}, oidcConfig{}, err
	}

	conn, err := r.reconcilePostgres(ctx, dp)
	if err != nil {
		return false, objectStore{}, oidcConfig{}, err
	}

	store, storeReady, err := r.reconcileObjectStore(ctx, dp)
	if err != nil {
		return false, objectStore{}, oidcConfig{}, err
	}

	oidc, authReady, err := r.reconcileAuth(ctx, dp)
	if err != nil {
		return false, store, oidc, err
	}

	if authReady {
		if err := r.reconcileLakekeeper(ctx, dp, conn, oidc); err != nil {
			return false, store, oidc, err
		}
	} else {
		setCondition(dp, dataplatformv1alpha1.ConditionLakekeeperReady, metav1.ConditionFalse, reasonNotReady, "Waiting for identity provider")
	}

	progressing := !conditionTrue(dp, dataplatformv1alpha1.ConditionPostgresReady) ||
		!conditionTrue(dp, dataplatformv1alpha1.ConditionLakekeeperReady) ||
		!authReady ||
		!storeReady
	if progressing {
		return true, store, oidc, nil
	}
	if err := r.reconcileWarehouse(ctx, dp, store, oidc); err != nil {
		return false, store, oidc, err
	}
	return !conditionTrue(dp, dataplatformv1alpha1.ConditionWarehouseReady), store, oidc, nil
}

func (r *DataPlatformReconciler) reconcileTrinoStack(ctx context.Context, dp *dataplatformv1alpha1.DataPlatform, store objectStore, oidc oidcConfig) (bool, error) {
	if !dp.Spec.Trino.IsEnabled() {
		setCondition(dp, dataplatformv1alpha1.ConditionTrinoReady, metav1.ConditionTrue, reasonDisabled, "Trino is disabled")
		return false, nil
	}

	ns := dp.Spec.Trino.NamespaceOrDefault()
	if err := r.ensureNamespace(ctx, dp, ns, componentTrinoCoordinator); err != nil {
		return false, err
	}
	if err := r.reconcileTrino(ctx, dp, store, oidc); err != nil {
		return false, err
	}
	return !conditionTrue(dp, dataplatformv1alpha1.ConditionTrinoReady), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DataPlatformReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dataplatformv1alpha1.DataPlatform{}).
		Owns(&corev1.Namespace{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Owns(&batchv1.Job{}).
		Named("dataplatform").
		Complete(r)
}
