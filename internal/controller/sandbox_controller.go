package controller

import (
	"context"
	"fmt"

	"github.com/gsim/perilinkle/internal/keycloak"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const SandboxFinalizer = "openshell.dev/keycloak-client"

var AgentSandboxGVK = schema.GroupVersionKind{
	Group:   "agents.x-k8s.io",
	Version: "v1alpha1",
	Kind:    "Sandbox",
}

type SandboxReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Keycloak keycloak.Reconciler
	Config   SandboxConfig
}

type SandboxConfig struct {
	ManagedLabel      string
	ManagedLabelValue string
	Realm             string
	SPIFFETrustDomain string
}

func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	resource := agentSandboxObject()
	if err := r.Get(ctx, req.NamespacedName, resource); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if resource.GetLabels()[r.Config.ManagedLabel] != r.Config.ManagedLabelValue {
		return ctrl.Result{}, nil
	}

	sandboxID := sandboxID(resource)
	if sandboxID == "" {
		return ctrl.Result{}, nil
	}

	sandbox := keycloak.Sandbox{
		ID:            sandboxID,
		Namespace:     resource.GetNamespace(),
		Name:          resource.GetName(),
		SPIFFESubject: sandboxSPIFFESubject(r.Config.SPIFFETrustDomain, resource.GetNamespace(), sandboxID),
	}
	realm := configuredRealm(r.Config.Realm)

	if resource.GetDeletionTimestamp() != nil {
		if !containsFinalizer(resource, SandboxFinalizer) {
			return ctrl.Result{}, nil
		}
		if err := r.Keycloak.DeleteSandbox(ctx, realm, sandbox); err != nil {
			return ctrl.Result{}, fmt.Errorf("delete sandbox keycloak client: %w", err)
		}
		removeFinalizer(resource, SandboxFinalizer)
		if err := r.Update(ctx, resource); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !containsFinalizer(resource, SandboxFinalizer) {
		addFinalizer(resource, SandboxFinalizer)
		if err := r.Update(ctx, resource); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.Keycloak.EnsureRealm(ctx, realm); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure namespace keycloak realm: %w", err)
	}
	if err := r.Keycloak.EnsureSandbox(ctx, realm, sandbox); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure sandbox keycloak client: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *SandboxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(agentSandboxObject()).
		Complete(r)
}

func agentSandboxObject() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(AgentSandboxGVK)
	return obj
}

func sandboxID(resource *unstructured.Unstructured) string {
	return resource.GetLabels()[LabelSandboxID]
}

func sandboxSPIFFESubject(trustDomain, namespace, sandboxID string) string {
	return fmt.Sprintf("%s/%s/sandbox/%s", trustDomain, namespace, sandboxID)
}

func containsFinalizer(obj client.Object, finalizer string) bool {
	for _, existing := range obj.GetFinalizers() {
		if existing == finalizer {
			return true
		}
	}
	return false
}

func addFinalizer(obj client.Object, finalizer string) {
	if containsFinalizer(obj, finalizer) {
		return
	}
	obj.SetFinalizers(append(obj.GetFinalizers(), finalizer))
}

func removeFinalizer(obj client.Object, finalizer string) {
	finalizers := obj.GetFinalizers()
	next := finalizers[:0]
	for _, existing := range finalizers {
		if existing != finalizer {
			next = append(next, existing)
		}
	}
	obj.SetFinalizers(next)
}
