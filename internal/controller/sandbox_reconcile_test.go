package controller

import (
	"context"
	"testing"
	"time"

	"github.com/gsim/perilinkle/internal/keycloak"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var v1beta1SandboxGVK = schema.GroupVersionKind{Group: "agents.x-k8s.io", Version: "v1beta1", Kind: "Sandbox"}

type sandboxKeycloak struct {
	ensured []keycloak.Sandbox
	deleted []keycloak.Sandbox
}

func (r *sandboxKeycloak) EnsureRealm(context.Context, keycloak.Realm) error { return nil }
func (r *sandboxKeycloak) EnsureTargetService(context.Context, keycloak.Realm, keycloak.TargetService) error {
	return nil
}
func (r *sandboxKeycloak) EnsureSandbox(_ context.Context, _ keycloak.Realm, s keycloak.Sandbox) error {
	r.ensured = append(r.ensured, s)
	return nil
}
func (r *sandboxKeycloak) DeleteSandbox(_ context.Context, _ keycloak.Realm, s keycloak.Sandbox) error {
	r.deleted = append(r.deleted, s)
	return nil
}
func (r *sandboxKeycloak) EnsureUsers(context.Context, keycloak.Realm, []keycloak.User) error {
	return nil
}

func newSandbox(name string, labels map[string]string, finalizers ...string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(v1beta1SandboxGVK)
	obj.SetNamespace("openshell-e2e")
	obj.SetName(name)
	obj.SetLabels(labels)
	obj.SetFinalizers(finalizers)
	return obj
}

func newReconciler(t *testing.T, kc *sandboxKeycloak, objs ...client.Object) *SandboxReconciler {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(objs...).Build()
	tmpl, err := NewSPIFFEIDTemplate("")
	if err != nil {
		t.Fatalf("NewSPIFFEIDTemplate: %v", err)
	}
	return &SandboxReconciler{
		Client:   c,
		Keycloak: kc,
		Config: SandboxConfig{
			ManagedLabel:      LabelManaged,
			ManagedLabelValue: "openshell",
			Realm:             "openshell",
			SPIFFETrustDomain: "spiffe://openshell.local",
			SandboxGVK:        v1beta1SandboxGVK,
			SPIFFEIDTemplate:  tmpl,
		},
	}
}

func reconcile(t *testing.T, r *SandboxReconciler, name string) {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "openshell-e2e", Name: name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func getSandbox(t *testing.T, r *SandboxReconciler, name string) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(v1beta1SandboxGVK)
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "openshell-e2e", Name: name}, obj); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	return obj
}

func TestReconcileRegistersManagedV1beta1Sandbox(t *testing.T) {
	kc := &sandboxKeycloak{}
	sb := newSandbox("default--agent-a", map[string]string{LabelManaged: "openshell", LabelSandboxID: "9c4daea2"})
	r := newReconciler(t, kc, sb)

	reconcile(t, r, "default--agent-a")

	if len(kc.ensured) != 1 || kc.ensured[0].ID != "9c4daea2" {
		t.Fatalf("ensured = %+v, want one registration for sandbox 9c4daea2", kc.ensured)
	}
	if !containsFinalizer(getSandbox(t, r, "default--agent-a"), SandboxFinalizer) {
		t.Fatal("finalizer not added to managed sandbox")
	}
}

func TestReconcileRegistersSPIFFEIDFromConfiguredTemplate(t *testing.T) {
	kc := &sandboxKeycloak{}
	sb := newSandbox("default--agent-a", map[string]string{LabelManaged: "openshell", LabelSandboxID: "9c4daea2"})
	r := newReconciler(t, kc, sb)

	reconcile(t, r, "default--agent-a")

	want := "spiffe://openshell.local/openshell/sandbox/9c4daea2"
	if len(kc.ensured) != 1 || kc.ensured[0].SPIFFESubject != want {
		t.Fatalf("ensured = %+v, want SPIFFESubject %q", kc.ensured, want)
	}
}

func TestReconcileIgnoresUnmanagedSandbox(t *testing.T) {
	kc := &sandboxKeycloak{}
	sb := newSandbox("other", map[string]string{LabelSandboxID: "x"})
	r := newReconciler(t, kc, sb)

	reconcile(t, r, "other")

	if len(kc.ensured) != 0 {
		t.Fatalf("unmanaged sandbox was registered: %+v", kc.ensured)
	}
	if containsFinalizer(getSandbox(t, r, "other"), SandboxFinalizer) {
		t.Fatal("finalizer added to unmanaged sandbox")
	}
}

func TestReconcileDeletingSandboxRemovesClientThenFinalizer(t *testing.T) {
	kc := &sandboxKeycloak{}
	sb := newSandbox("default--agent-b", map[string]string{LabelManaged: "openshell", LabelSandboxID: "3c1cfe14"}, SandboxFinalizer)
	now := metav1.NewTime(time.Now())
	sb.SetDeletionTimestamp(&now)
	r := newReconciler(t, kc, sb)

	reconcile(t, r, "default--agent-b")

	if len(kc.deleted) != 1 || kc.deleted[0].ID != "3c1cfe14" {
		t.Fatalf("deleted = %+v, want one deletion for sandbox 3c1cfe14", kc.deleted)
	}
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(v1beta1SandboxGVK)
	err := r.Get(context.Background(), types.NamespacedName{Namespace: "openshell-e2e", Name: "default--agent-b"}, obj)
	if err == nil && containsFinalizer(obj, SandboxFinalizer) {
		t.Fatal("finalizer still present after Keycloak cleanup")
	}
}
