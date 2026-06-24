package controller

import (
	"context"
	"fmt"

	"github.com/gsim/perilinkle/api/v1alpha1"
	"github.com/gsim/perilinkle/internal/keycloak"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ServiceGroupReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Keycloak         keycloak.Reconciler
	OpenShellGateway OpenShellGateway
	Config           ServiceGroupConfig
}

const ServiceGroupProviderProfileFinalizer = "perilinkle.dev/provider-profile"

type ServiceGroupConfig struct {
	ManagedLabel      string
	ManagedLabelValue string
	KeycloakURL       string
	KeycloakPublicURL string
	Realm             string
	SPIFFETrustDomain string
}

func (r *ServiceGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var resource v1alpha1.ServiceGroup
	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	realm := configuredRealm(r.Config.Realm)
	if resource.GetDeletionTimestamp() != nil {
		if !containsFinalizer(&resource, ServiceGroupProviderProfileFinalizer) {
			return ctrl.Result{}, nil
		}
		if r.OpenShellGateway == nil {
			return ctrl.Result{}, fmt.Errorf("delete openshell provider profile: openshell gateway address is not configured")
		}
		if err := r.OpenShellGateway.DeleteProviderProfile(ctx, providerProfileID(&resource)); err != nil {
			return ctrl.Result{}, fmt.Errorf("delete openshell provider profile %q: %w", providerProfileID(&resource), err)
		}
		if err := r.patchServiceGroupFinalizer(ctx, &resource, false); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	for _, spec := range resource.Spec.TargetServices {
		target := keycloak.TargetService{
			ClientID:           profileTargetClientID(spec),
			Namespace:          profileTargetNamespace(resource.Namespace, spec),
			ServiceName:        spec.Name,
			ServiceAccountName: profileTargetServiceAccount(spec),
			SPIFFESubject:      profileTargetSPIFFESubject(r.Config.SPIFFETrustDomain, resource.Namespace, spec),
		}
		if err := r.Keycloak.EnsureTargetService(ctx, realm, target); err != nil {
			_ = r.updateServiceGroupStatus(ctx, &resource, false, "KeycloakReconcileFailed", err.Error(), "", "")
			return ctrl.Result{}, fmt.Errorf("ensure target service %q keycloak client: %w", spec.Name, err)
		}
	}

	openShellProfile, err := r.openShellProviderProfile(&resource, realm)
	if err != nil {
		_ = r.updateServiceGroupStatus(ctx, &resource, false, "ProviderProfileBuildFailed", err.Error(), "", "")
		return ctrl.Result{}, err
	}
	profileResourceVersion := ""
	if openShellProfile != nil {
		if r.OpenShellGateway == nil {
			err := fmt.Errorf("openshell gateway address is not configured")
			_ = r.updateServiceGroupStatus(ctx, &resource, false, "GatewayReconcileFailed", err.Error(), openShellProfile.ID, "")
			return ctrl.Result{}, err
		}
		if !containsFinalizer(&resource, ServiceGroupProviderProfileFinalizer) {
			if err := r.patchServiceGroupFinalizer(ctx, &resource, true); err != nil {
				return ctrl.Result{}, err
			}
		}
		var err error
		profileResourceVersion, err = r.OpenShellGateway.UpsertProviderProfile(ctx, openShellProfile)
		if err != nil {
			_ = r.updateServiceGroupStatus(ctx, &resource, false, "GatewayReconcileFailed", err.Error(), openShellProfile.ID, "")
			return ctrl.Result{}, fmt.Errorf("upsert openshell provider profile %q: %w", openShellProfile.ID, err)
		}
	} else if containsFinalizer(&resource, ServiceGroupProviderProfileFinalizer) {
		if r.OpenShellGateway == nil {
			err := fmt.Errorf("openshell gateway address is not configured")
			_ = r.updateServiceGroupStatus(ctx, &resource, false, "GatewayReconcileFailed", err.Error(), providerProfileID(&resource), "")
			return ctrl.Result{}, err
		}
		if err := r.OpenShellGateway.DeleteProviderProfile(ctx, providerProfileID(&resource)); err != nil {
			_ = r.updateServiceGroupStatus(ctx, &resource, false, "GatewayReconcileFailed", err.Error(), providerProfileID(&resource), "")
			return ctrl.Result{}, fmt.Errorf("delete disabled openshell provider profile %q: %w", providerProfileID(&resource), err)
		}
		if err := r.patchServiceGroupFinalizer(ctx, &resource, false); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := r.reconcileExistingSandboxes(ctx, realm); err != nil {
		_ = r.updateServiceGroupStatus(ctx, &resource, false, "KeycloakReconcileFailed", err.Error(), "", "")
		return ctrl.Result{}, err
	}
	profileID := ""
	if openShellProfile != nil {
		profileID = openShellProfile.ID
	}
	if err := r.updateServiceGroupStatus(ctx, &resource, true, "Reconciled", "ServiceGroup reconciled", profileID, profileResourceVersion); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ServiceGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ServiceGroup{}).
		Complete(r)
}

func (r *ServiceGroupReconciler) patchServiceGroupFinalizer(ctx context.Context, group *v1alpha1.ServiceGroup, present bool) error {
	base := group.DeepCopy()
	if present {
		addFinalizer(group, ServiceGroupProviderProfileFinalizer)
	} else {
		removeFinalizer(group, ServiceGroupProviderProfileFinalizer)
	}
	return r.Patch(ctx, group, client.MergeFrom(base))
}

func (r *ServiceGroupReconciler) reconcileExistingSandboxes(ctx context.Context, realm keycloak.Realm) error {
	sandboxes := &unstructured.UnstructuredList{}
	sandboxes.SetGroupVersionKind(AgentSandboxGVK.GroupVersion().WithKind(AgentSandboxGVK.Kind + "List"))
	if err := r.List(ctx, sandboxes, client.MatchingLabels{
		r.Config.ManagedLabel: r.Config.ManagedLabelValue,
	}); err != nil {
		return fmt.Errorf("list managed sandboxes: %w", err)
	}

	for i := range sandboxes.Items {
		resource := &sandboxes.Items[i]
		sandboxID := sandboxID(resource)
		if sandboxID == "" {
			continue
		}
		sandbox := keycloak.Sandbox{
			ID:            sandboxID,
			Namespace:     resource.GetNamespace(),
			Name:          resource.GetName(),
			SPIFFESubject: sandboxSPIFFESubject(r.Config.SPIFFETrustDomain, resource.GetNamespace(), sandboxID),
		}
		if err := r.Keycloak.EnsureSandbox(ctx, realm, sandbox); err != nil {
			return fmt.Errorf("ensure sandbox %s/%s default scopes: %w", sandbox.Namespace, sandbox.Name, err)
		}
	}
	return nil
}

func profileTargetClientID(spec v1alpha1.TargetService) string {
	if spec.ClientID != "" {
		return spec.ClientID
	}
	return spec.Name
}

func profileTargetNamespace(profileNamespace string, spec v1alpha1.TargetService) string {
	if spec.Namespace != "" {
		return spec.Namespace
	}
	return profileNamespace
}

func profileTargetServiceAccount(spec v1alpha1.TargetService) string {
	if spec.ServiceAccountName != "" {
		return spec.ServiceAccountName
	}
	return spec.Name
}

func profileTargetSPIFFESubject(trustDomain, profileNamespace string, spec v1alpha1.TargetService) string {
	if spec.SPIFFEID != "" {
		return spec.SPIFFEID
	}
	return fmt.Sprintf("%s/ns/%s/sa/%s", trustDomain, profileTargetNamespace(profileNamespace, spec), profileTargetServiceAccount(spec))
}

func serviceGroupProviderProfileName(group *v1alpha1.ServiceGroup) string {
	if group.Spec.ProviderProfileName != "" {
		return group.Spec.ProviderProfileName
	}
	return group.Name
}

func (r *ServiceGroupReconciler) updateServiceGroupStatus(ctx context.Context, group *v1alpha1.ServiceGroup, ready bool, reason, message, profileID, profileResourceVersion string) error {
	next := group.DeepCopy()
	next.Status.ObservedGeneration = group.Generation
	next.Status.Ready = ready
	next.Status.ProviderProfileID = profileID
	next.Status.ProviderProfileResourceVersion = profileResourceVersion
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	apimeta.SetStatusCondition(&next.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		ObservedGeneration: group.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	})
	return r.Status().Update(ctx, next)
}
