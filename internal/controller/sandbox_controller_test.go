package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestSandboxIDComesFromRequiredLabel(t *testing.T) {
	resource := &unstructured.Unstructured{}
	resource.SetName("object-name")
	resource.SetLabels(map[string]string{LabelSandboxID: "sandbox-123"})

	if got := sandboxID(resource); got != "sandbox-123" {
		t.Fatalf("sandboxID = %q, want %q", got, "sandbox-123")
	}
}

func TestSandboxIDMissingLabelIsEmpty(t *testing.T) {
	resource := &unstructured.Unstructured{}
	resource.SetName("object-name")

	if got := sandboxID(resource); got != "" {
		t.Fatalf("sandboxID = %q, want empty", got)
	}
}
