/*
Copyright 2025.

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
	"maps"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	authv1beta1 "open-cluster-management.io/managed-serviceaccount/apis/authentication/v1beta1"

	sveltosv1alpha1 "github.com/guilhem/sveltos-ocm-addon/api/v1alpha1"
)

const (
	// AddonName is the name of our addon
	AddonName = "sveltos-ocm-addon"
	// ManagedServiceAccountName is the name of the managed service account
	ManagedServiceAccountName = "sveltos-ocm"
	// FinalizerName is the finalizer added to SveltosOCMCluster
	FinalizerName = "sveltos.open-cluster-management.io/finalizer"
	// DefaultTokenValidity is the default token validity period
	DefaultTokenValidity = "168h" // 7 days
	// RBACManifestWorkName is the name of the ManifestWork for RBAC
	RBACManifestWorkName = "sveltos-ocm-rbac"
	// RBACClusterRoleName is the name of the ClusterRole on managed clusters
	RBACClusterRoleName = "sveltos-ocm-admin"
	// ManagedServiceAccountNamespace is the namespace where the ServiceAccount is created on managed clusters
	ManagedServiceAccountNamespace = "open-cluster-management-agent-addon"
)

// SveltosOCMClusterReconciler reconciles a SveltosOCMCluster object
type SveltosOCMClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sveltos.open-cluster-management.io,resources=sveltosocmclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sveltos.open-cluster-management.io,resources=sveltosocmclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sveltos.open-cluster-management.io,resources=sveltosocmclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=addon.open-cluster-management.io,resources=managedclusteraddons,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=addon.open-cluster-management.io,resources=managedclusteraddons/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=addon.open-cluster-management.io,resources=clustermanagementaddons,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=authentication.open-cluster-management.io,resources=managedserviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SveltosOCMClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the SveltosOCMCluster instance
	sveltosOCMCluster := &sveltosv1alpha1.SveltosOCMCluster{}
	if err := r.Get(ctx, req.NamespacedName, sveltosOCMCluster); err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return without error
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get SveltosOCMCluster")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !sveltosOCMCluster.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, sveltosOCMCluster)
	}

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(sveltosOCMCluster, FinalizerName) {
		controllerutil.AddFinalizer(sveltosOCMCluster, FinalizerName)
		if err := r.Update(ctx, sveltosOCMCluster); err != nil {
			return ctrl.Result{}, err
		}
	}

	// List ManagedClusterAddOns for our addon using field selector
	addonList := &addonv1alpha1.ManagedClusterAddOnList{}
	if err := r.List(ctx, addonList, client.MatchingFields{"metadata.name": AddonName}); err != nil {
		log.Error(err, "Failed to list ManagedClusterAddOns")
		return ctrl.Result{}, err
	}

	// Process each managed cluster addon
	var registeredClusters []sveltosv1alpha1.RegisteredClusterInfo
	var requeue bool
	for i := range addonList.Items {
		addon := &addonList.Items[i]
		clusterName := addon.Namespace

		result, clusterInfo, err := r.reconcileCluster(ctx, sveltosOCMCluster, clusterName)
		if err != nil {
			log.Error(err, "Failed to reconcile cluster", "cluster", clusterName)
			// Update addon status to reflect the error
			if updateErr := r.updateManagedClusterAddOnStatus(ctx, addon, false, err.Error()); updateErr != nil {
				log.Error(updateErr, "Failed to update ManagedClusterAddOn status", "cluster", clusterName)
			}
			// Continue with other clusters but requeue
			requeue = true
			continue
		}

		if result.RequeueAfter > 0 {
			requeue = true
		}

		if clusterInfo != nil {
			registeredClusters = append(registeredClusters, *clusterInfo)
			// Update addon status to Available
			if updateErr := r.updateManagedClusterAddOnStatus(ctx, addon, true, "SveltosCluster created successfully"); updateErr != nil {
				log.Error(updateErr, "Failed to update ManagedClusterAddOn status", "cluster", clusterName)
			}
		}
	}

	// Update status
	sveltosOCMCluster.Status.RegisteredClusters = registeredClusters

	changed := false
	// Update conditions based on the state
	if len(registeredClusters) > 0 {
		changed = meta.SetStatusCondition(&sveltosOCMCluster.Status.Conditions, metav1.Condition{
			Type:               "Available",
			Status:             metav1.ConditionTrue,
			Reason:             "ClustersRegistered",
			Message:            fmt.Sprintf("Successfully registered %d cluster(s)", len(registeredClusters)),
			ObservedGeneration: sveltosOCMCluster.Generation,
		})
	} else {
		changed = meta.SetStatusCondition(&sveltosOCMCluster.Status.Conditions, metav1.Condition{
			Type:               "Available",
			Status:             metav1.ConditionFalse,
			Reason:             "NoClusters",
			Message:            "No managed clusters found",
			ObservedGeneration: sveltosOCMCluster.Generation,
		})
	}

	if changed {
		if err := r.Status().Update(ctx, sveltosOCMCluster); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
	}

	if requeue {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileCluster handles the reconciliation for a single cluster
func (r *SveltosOCMClusterReconciler) reconcileCluster(
	ctx context.Context,
	sveltosOCMCluster *sveltosv1alpha1.SveltosOCMCluster,
	clusterName string,
) (ctrl.Result, *sveltosv1alpha1.RegisteredClusterInfo, error) {
	log := logf.FromContext(ctx)

	clusterInfo := &sveltosv1alpha1.RegisteredClusterInfo{
		ClusterName:      clusterName,
		ClusterNamespace: clusterName,
	}

	// 0. Verify managed-serviceaccount addon is installed
	if err := r.checkManagedServiceAccountAddon(ctx, clusterName); err != nil {
		log.Error(err, "managed-serviceaccount addon check failed", "cluster", clusterName)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, clusterInfo, err
	}

	// 1. Ensure ManagedServiceAccount exists
	msa := &authv1beta1.ManagedServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ManagedServiceAccountName,
			Namespace: clusterName,
		},
	}

	msaOp, err := controllerutil.CreateOrUpdate(ctx, r.Client, msa, func() error {
		r.configureManagedServiceAccount(msa, sveltosOCMCluster)
		// Note: Can't use SetControllerReference here because ManagedServiceAccount
		// is in a different namespace than SveltosOCMCluster.
		// Cleanup is handled via finalizers in reconcileDelete.
		return nil
	})
	if err != nil {
		return ctrl.Result{}, nil, fmt.Errorf("failed to create or update ManagedServiceAccount: %w", err)
	}
	if msaOp == controllerutil.OperationResultCreated {
		log.Info("Created ManagedServiceAccount", "cluster", clusterName)
		// Requeue to wait for token
		return ctrl.Result{RequeueAfter: 10 * time.Second}, clusterInfo, nil
	}
	log.V(1).Info("ManagedServiceAccount reconciled", "cluster", clusterName, "operation", msaOp)

	// 1.5. Ensure RBAC ManifestWork exists to grant permissions on managed cluster
	if err := r.createOrUpdateRBACManifestWork(ctx, clusterName); err != nil {
		return ctrl.Result{}, nil, fmt.Errorf("failed to create or update RBAC ManifestWork: %w", err)
	}

	// 2. Check if token is ready
	if msa.Status.TokenSecretRef == nil || msa.Status.TokenSecretRef.Name == "" {
		log.Info("Waiting for ManagedServiceAccount token", "cluster", clusterName)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, clusterInfo, nil
	}

	clusterInfo.TokenSecretRef = &msa.Status.TokenSecretRef.Name
	if msa.Status.ExpirationTimestamp != nil {
		clusterInfo.ExpirationTime = &metav1.Time{Time: msa.Status.ExpirationTimestamp.Time}
	}

	// 3. Get the token secret
	tokenSecret := &corev1.Secret{}
	tokenSecretName := types.NamespacedName{
		Name:      msa.Status.TokenSecretRef.Name,
		Namespace: clusterName,
	}
	if err := r.Get(ctx, tokenSecretName, tokenSecret); err != nil {
		log.Error(err, "Failed to get token secret", "secret", tokenSecretName)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, clusterInfo, nil
	}

	// 4. Get ManagedCluster to get API server URL and CA
	managedCluster := &clusterv1.ManagedCluster{}
	if err := r.Get(ctx, types.NamespacedName{Name: clusterName}, managedCluster); err != nil {
		log.Error(err, "Failed to get ManagedCluster", "cluster", clusterName)
		return ctrl.Result{}, nil, err
	}

	// 5. Create or update SveltosCluster
	// Use the OCM cluster namespace (same as ManagedClusterAddOn namespace)
	sveltosNamespace := clusterName

	sveltosCluster, err := r.createOrUpdateSveltosCluster(
		ctx,
		sveltosOCMCluster,
		managedCluster,
		tokenSecret,
		sveltosNamespace,
		clusterName,
	)
	if err != nil {
		return ctrl.Result{}, nil, err
	}

	clusterInfo.SveltosClusterCreated = sveltosCluster != nil

	log.Info("Successfully reconciled cluster", "cluster", clusterName)
	return ctrl.Result{}, clusterInfo, nil
}

// updateManagedClusterAddOnStatus updates the ManagedClusterAddOn status to report health
// This is required because sveltos-ocm-addon is a hub-only controller (no agent on managed clusters)
// and uses Customized health check mode instead of Lease-based health reporting.
func (r *SveltosOCMClusterReconciler) updateManagedClusterAddOnStatus(
	ctx context.Context,
	addon *addonv1alpha1.ManagedClusterAddOn,
	available bool,
	message string,
) error {
	// Set health check mode to Customized (hub manages health status)
	addon.Status.HealthCheck.Mode = addonv1alpha1.HealthCheckModeCustomized

	// Update the Available condition
	status := metav1.ConditionFalse
	reason := "AddonNotReady"
	if available {
		status = metav1.ConditionTrue
		reason = "AddonReady"
	}

	if meta.SetStatusCondition(&addon.Status.Conditions, metav1.Condition{
		Type:               addonv1alpha1.ManagedClusterAddOnConditionAvailable,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: addon.Generation,
	}) {
		return r.Status().Update(ctx, addon)
	}

	return nil
}

// configureManagedServiceAccount configures a ManagedServiceAccount resource
func (r *SveltosOCMClusterReconciler) configureManagedServiceAccount(
	msa *authv1beta1.ManagedServiceAccount,
	sveltosOCMCluster *sveltosv1alpha1.SveltosOCMCluster,
) {
	// Use the token validity from the spec, or use the default if not specified
	validity := sveltosOCMCluster.Spec.TokenValidity
	if validity.Duration == 0 {
		// Parse the default validity duration
		defaultDuration, err := time.ParseDuration(DefaultTokenValidity)
		if err == nil {
			validity = metav1.Duration{Duration: defaultDuration}
		}
	}

	msa.Spec = authv1beta1.ManagedServiceAccountSpec{
		Rotation: authv1beta1.ManagedServiceAccountRotation{
			Enabled:  true,
			Validity: validity,
		},
	}
}

// createOrUpdateRBACManifestWork creates or updates a ManifestWork to deploy RBAC resources
// to the managed cluster, granting the ManagedServiceAccount the necessary permissions.
func (r *SveltosOCMClusterReconciler) createOrUpdateRBACManifestWork(
	ctx context.Context,
	clusterName string,
) error {
	log := logf.FromContext(ctx)

	// Build the ClusterRole
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: RBACClusterRoleName,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
		},
	}

	// Build the ClusterRoleBinding
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: RBACClusterRoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      ManagedServiceAccountName,
				Namespace: ManagedServiceAccountNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     RBACClusterRoleName,
		},
	}

	// Serialize the resources to JSON using the scheme (auto-injects GVK)
	serializer := kjson.NewSerializerWithOptions(
		kjson.DefaultMetaFactory,
		r.Scheme, // creater
		r.Scheme, // typer - used to look up GVK
		kjson.SerializerOptions{},
	)

	clusterRoleRaw, err := runtime.Encode(serializer, clusterRole)
	if err != nil {
		return fmt.Errorf("failed to encode ClusterRole: %w", err)
	}

	clusterRoleBindingRaw, err := runtime.Encode(serializer, clusterRoleBinding)
	if err != nil {
		return fmt.Errorf("failed to encode ClusterRoleBinding: %w", err)
	}

	// Create or update the ManifestWork
	manifestWork := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RBACManifestWorkName,
			Namespace: clusterName,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, manifestWork, func() error {
		manifestWork.Spec.Workload.Manifests = []workv1.Manifest{
			{RawExtension: runtime.RawExtension{Raw: clusterRoleRaw}},
			{RawExtension: runtime.RawExtension{Raw: clusterRoleBindingRaw}},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update RBAC ManifestWork: %w", err)
	}

	log.Info("RBAC ManifestWork reconciled", "cluster", clusterName, "operation", op)
	return nil
}

// checkManagedServiceAccountAddon verifies that the managed-serviceaccount addon is installed
// and creates the ManagedClusterAddOn if it doesn't exist
func (r *SveltosOCMClusterReconciler) checkManagedServiceAccountAddon(
	ctx context.Context,
	clusterName string,
) error {
	log := logf.FromContext(ctx)

	// First check if ClusterManagementAddOn exists (addon installed on hub)
	cma := &addonv1alpha1.ClusterManagementAddOn{}
	if err := r.Get(ctx, types.NamespacedName{Name: "managed-serviceaccount"}, cma); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("managed-serviceaccount ClusterManagementAddOn not found. Please install the managed-serviceaccount addon on the hub: https://open-cluster-management.io/getting-started/integration/managed-serviceaccount/")
		}
		return fmt.Errorf("failed to get managed-serviceaccount ClusterManagementAddOn: %w", err)
	}

	// Create or get ManagedClusterAddOn for this cluster
	addon := &addonv1alpha1.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "managed-serviceaccount",
			Namespace: clusterName,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, addon, func() error {
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create/update managed-serviceaccount ManagedClusterAddOn: %w", err)
	}
	if op == controllerutil.OperationResultCreated {
		log.Info("Created managed-serviceaccount ManagedClusterAddOn", "cluster", clusterName)
		// Return error to requeue and wait for addon to be ready
		return fmt.Errorf("managed-serviceaccount addon just created, waiting for it to become available")
	}

	if !meta.IsStatusConditionTrue(addon.Status.Conditions, addonv1alpha1.ManagedClusterAddOnConditionAvailable) {
		return fmt.Errorf("managed-serviceaccount addon is not yet available")
	}

	return nil
}

// createOrUpdateSveltosCluster creates or updates a SveltosCluster and its kubeconfig secret
func (r *SveltosOCMClusterReconciler) createOrUpdateSveltosCluster(
	ctx context.Context,
	sveltosOCMCluster *sveltosv1alpha1.SveltosOCMCluster,
	managedCluster *clusterv1.ManagedCluster,
	tokenSecret *corev1.Secret,
	sveltosNamespace string,
	clusterName string,
) (*libsveltosv1beta1.SveltosCluster, error) {
	log := logf.FromContext(ctx)

	// Note: We use the OCM cluster namespace which already exists (created by OCM)
	// No need to create it ourselves

	// Extract token from the secret
	token, ok := tokenSecret.Data["token"]
	if !ok {
		return nil, fmt.Errorf("token not found in secret")
	}

	// Use cluster-proxy (prerequisite)
	proxyNamespace := "open-cluster-management-addon"
	apiServerURL := fmt.Sprintf("https://cluster-proxy-addon-user.%s.svc:9092/%s", proxyNamespace, clusterName)

	// Get the CA from the cluster-proxy-user-serving-cert secret
	proxyCASecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: proxyNamespace,
		Name:      "cluster-proxy-user-serving-cert",
	}, proxyCASecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster-proxy CA cert (is cluster-proxy installed with enableServiceProxy=true?): %w", err)
	}

	ca, ok := proxyCASecret.Data["ca.crt"]
	if !ok {
		return nil, fmt.Errorf("ca.crt not found in cluster-proxy-user-serving-cert secret")
	}

	// Create kubeconfig
	kubeconfig, err := createKubeconfig(apiServerURL, ca, token, clusterName)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubeconfig: %w", err)
	}

	// Create or update kubeconfig secret
	kubeconfigSecretName := defaultSveltosKubeconfigName(clusterName)
	kubeconfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeconfigSecretName,
			Namespace: sveltosNamespace,
		},
	}

	secretOp, err := controllerutil.CreateOrUpdate(ctx, r.Client, kubeconfigSecret, func() error {
		kubeconfigSecret.Type = corev1.SecretTypeOpaque
		kubeconfigSecret.Data = map[string][]byte{
			"kubeconfig": kubeconfig,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update kubeconfig secret: %w", err)
	}
	log.Info("Kubeconfig secret reconciled", "secret", kubeconfigSecretName, "operation", secretOp)

	// Create or update SveltosCluster
	sveltosCluster := &libsveltosv1beta1.SveltosCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: sveltosNamespace,
		},
	}

	clusterOp, err := controllerutil.CreateOrUpdate(ctx, r.Client, sveltosCluster, func() error {
		sveltosCluster.Spec = libsveltosv1beta1.SveltosClusterSpec{
			KubeconfigName:    kubeconfigSecretName,
			KubeconfigKeyName: "kubeconfig",
		}

		// Sync labels if enabled
		if sveltosOCMCluster.Spec.LabelSync {
			if sveltosCluster.Labels == nil {
				sveltosCluster.Labels = make(map[string]string)
			}
			maps.Copy(sveltosCluster.Labels, managedCluster.Labels)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update SveltosCluster: %w", err)
	}
	log.Info("SveltosCluster reconciled", "cluster", clusterName, "operation", clusterOp)

	return sveltosCluster, nil
}

// reconcileDelete handles deletion of the SveltosOCMCluster
func (r *SveltosOCMClusterReconciler) reconcileDelete(
	ctx context.Context,
	sveltosOCMCluster *sveltosv1alpha1.SveltosOCMCluster,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Clean up all managed resources
	// Use the OCM cluster namespace (same as ManagedClusterAddOn namespace)
	for _, cluster := range sveltosOCMCluster.Status.RegisteredClusters {
		sveltosNamespace := cluster.ClusterNamespace

		kubeconfigSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultSveltosKubeconfigName(cluster.ClusterName),
				Namespace: sveltosNamespace,
			},
		}

		sveltosCluster := &libsveltosv1beta1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.ClusterName,
				Namespace: sveltosNamespace,
			},
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(sveltosCluster), sveltosCluster); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to get SveltosCluster", "cluster", cluster.ClusterName)
				continue
			}
		} else {
			kubeconfigSecret.Name = sveltosCluster.Spec.KubeconfigName
		}

		// Delete ManagedServiceAccount
		msa := &authv1beta1.ManagedServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ManagedServiceAccountName,
				Namespace: cluster.ClusterNamespace,
			},
		}
		if err := r.Delete(ctx, msa); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to delete ManagedServiceAccount", "cluster", cluster.ClusterName)
		}

		// Delete RBAC ManifestWork
		manifestWork := &workv1.ManifestWork{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RBACManifestWorkName,
				Namespace: cluster.ClusterNamespace,
			},
		}
		if err := r.Delete(ctx, manifestWork); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to delete RBAC ManifestWork", "cluster", cluster.ClusterName)
		}

		// Delete kubeconfig secret
		if err := r.Delete(ctx, kubeconfigSecret); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to delete kubeconfig secret", "cluster", cluster.ClusterName)
		}

		// Delete SveltosCluster
		if err := r.Delete(ctx, sveltosCluster); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to delete SveltosCluster", "cluster", cluster.ClusterName)
		}

	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(sveltosOCMCluster, FinalizerName)
	if err := r.Update(ctx, sveltosOCMCluster); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Successfully deleted SveltosOCMCluster")
	return ctrl.Result{}, nil
}

// createKubeconfig creates a kubeconfig file from the given parameters
func createKubeconfig(apiServerURL string, ca, token []byte, clusterName string) ([]byte, error) {
	config := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			clusterName: {
				Server:                   apiServerURL,
				CertificateAuthorityData: ca,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			clusterName: {
				Token: string(token),
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			clusterName: {
				Cluster:  clusterName,
				AuthInfo: clusterName,
			},
		},
		CurrentContext: clusterName,
	}

	return clientcmd.Write(config)
}

func defaultSveltosKubeconfigName(clusterName string) string {
	return fmt.Sprintf("%s-sveltos-kubeconfig", clusterName)
}

// SetupWithManager sets up the controller with the Manager.
func (r *SveltosOCMClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&addonv1alpha1.ManagedClusterAddOn{},
		"metadata.name",
		func(o client.Object) []string { return []string{o.GetName()} },
	); err != nil {
		return fmt.Errorf("failed to index ManagedClusterAddOn by name: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sveltosv1alpha1.SveltosOCMCluster{}).
		// Watch ManagedClusterAddOn to trigger reconciliation when addons are created/deleted
		Watches(
			&addonv1alpha1.ManagedClusterAddOn{},
			handler.EnqueueRequestsFromMapFunc(r.findSveltosOCMClusterForAddon),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
				return object.GetName() == AddonName
			})),
		).
		// Note: Not using Owns() for ManagedServiceAccount because they are in
		// different namespaces and cross-namespace owner references are not allowed.
		Named("sveltosocmcluster").
		Complete(r)
}

// findSveltosOCMClusterForAddon maps a ManagedClusterAddOn to the referenced SveltosOCMCluster(s)
func (r *SveltosOCMClusterReconciler) findSveltosOCMClusterForAddon(_ context.Context, obj client.Object) []ctrl.Request {
	addon, ok := obj.(*addonv1alpha1.ManagedClusterAddOn)
	if !ok {
		return nil
	}

	// Find all SveltosOCMCluster references in configReferences
	var requests []ctrl.Request
	for _, configRef := range addon.Status.ConfigReferences {
		if configRef.Group == sveltosv1alpha1.GroupVersion.Group &&
			configRef.Resource == "sveltosocmclusters" {
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      configRef.Name,
					Namespace: configRef.Namespace,
				},
			})
		}
	}

	return requests
}
