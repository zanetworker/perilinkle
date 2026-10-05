package controller

import (
	"testing"

	"github.com/gsim/perilinkle/api/v1alpha1"
)

// Current OpenShell rejects tls: none ("unknown tls value"); an omitted value keeps
// automatic TLS termination.

func TestEndpointOmitsTLSByDefault(t *testing.T) {
	ep := openShellEndpointForTarget("default", v1alpha1.ProviderProfileEndpointDefaults{}, v1alpha1.TargetService{Name: "alpha"})
	if ep.TLS != "" {
		t.Fatalf("TLS = %q, want empty (omitted)", ep.TLS)
	}
}

func TestEndpointNormalizesLegacyTLSNone(t *testing.T) {
	ep := openShellEndpointForTarget("default", v1alpha1.ProviderProfileEndpointDefaults{TLS: "none"}, v1alpha1.TargetService{Name: "alpha"})
	if ep.TLS != "" {
		t.Fatalf("TLS = %q, want legacy none normalized to empty", ep.TLS)
	}
}

func TestEndpointKeepsExplicitTLSValue(t *testing.T) {
	target := v1alpha1.TargetService{Name: "alpha", Endpoint: v1alpha1.TargetServiceEndpoint{TLS: "skip"}}
	ep := openShellEndpointForTarget("default", v1alpha1.ProviderProfileEndpointDefaults{TLS: "none"}, target)
	if ep.TLS != "skip" {
		t.Fatalf("TLS = %q, want explicit per-target value skip", ep.TLS)
	}
}
