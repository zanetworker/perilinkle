package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestResolveSandboxGVKPrefersV1beta1(t *testing.T) {
	gvk, err := resolveSandboxGVK([]string{"v1alpha1", "v1beta1"}, "")
	if err != nil {
		t.Fatalf("resolveSandboxGVK error: %v", err)
	}
	if gvk.Version != "v1beta1" || gvk.Group != "agents.x-k8s.io" || gvk.Kind != "Sandbox" {
		t.Fatalf("gvk = %v, want agents.x-k8s.io/v1beta1 Sandbox", gvk)
	}
}

func TestResolveSandboxGVKFallsBackToV1alpha1(t *testing.T) {
	gvk, err := resolveSandboxGVK([]string{"v1alpha1"}, "")
	if err != nil {
		t.Fatalf("resolveSandboxGVK error: %v", err)
	}
	if gvk.Version != "v1alpha1" {
		t.Fatalf("version = %q, want v1alpha1", gvk.Version)
	}
}

func TestResolveSandboxGVKErrorsWhenNoSupportedVersionServed(t *testing.T) {
	if _, err := resolveSandboxGVK([]string{"v2"}, ""); err == nil {
		t.Fatal("expected error when neither v1beta1 nor v1alpha1 is served")
	}
	if _, err := resolveSandboxGVK(nil, ""); err == nil {
		t.Fatal("expected error when Agent Sandbox is not installed")
	}
}

func TestResolveSandboxGVKHonorsServedOverride(t *testing.T) {
	gvk, err := resolveSandboxGVK([]string{"v1alpha1", "v1beta1"}, "v1alpha1")
	if err != nil {
		t.Fatalf("resolveSandboxGVK error: %v", err)
	}
	if gvk.Version != "v1alpha1" {
		t.Fatalf("version = %q, want override v1alpha1", gvk.Version)
	}
}

func TestResolveSandboxGVKRejectsUnservedOverride(t *testing.T) {
	if _, err := resolveSandboxGVK([]string{"v1beta1"}, "v1alpha1"); err == nil {
		t.Fatal("expected error for an override the cluster does not serve")
	}
}

func TestServedSandboxVersionsListsOnlyVersionsWithSandboxKind(t *testing.T) {
	dc := &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{}}
	dc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "agents.x-k8s.io/v1beta1", APIResources: []metav1.APIResource{{Name: "sandboxes", Kind: "Sandbox"}}},
		{GroupVersion: "agents.x-k8s.io/v1alpha1", APIResources: []metav1.APIResource{{Name: "sandboxes", Kind: "Sandbox"}}},
		{GroupVersion: "agents.x-k8s.io/v1", APIResources: []metav1.APIResource{{Name: "sandboxclaims", Kind: "SandboxClaim"}}},
		{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{{Name: "deployments", Kind: "Deployment"}}},
	}

	got, err := servedSandboxVersions(dc)
	if err != nil {
		t.Fatalf("servedSandboxVersions error: %v", err)
	}
	if len(got) != 2 || !contains(got, "v1beta1") || !contains(got, "v1alpha1") {
		t.Fatalf("served = %v, want [v1beta1 v1alpha1]", got)
	}
}
