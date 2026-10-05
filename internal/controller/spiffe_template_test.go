package controller

import "testing"

func TestDefaultSPIFFEIDTemplateMatchesOpenShellClusterSPIFFEID(t *testing.T) {
	tmpl, err := NewSPIFFEIDTemplate("")
	if err != nil {
		t.Fatalf("NewSPIFFEIDTemplate: %v", err)
	}
	got := tmpl.Render("spiffe://openshell.local", "openshell-e2e", "default--agent-a", "9c4daea2")
	want := "spiffe://openshell.local/openshell/sandbox/9c4daea2"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestSPIFFEIDTemplateRendersAllPlaceholders(t *testing.T) {
	tmpl, err := NewSPIFFEIDTemplate("{trustDomain}/{namespace}/{name}/sandbox/{sandboxID}")
	if err != nil {
		t.Fatalf("NewSPIFFEIDTemplate: %v", err)
	}
	got := tmpl.Render("spiffe://td", "ns-a", "sb-name", "id-1")
	if want := "spiffe://td/ns-a/sb-name/sandbox/id-1"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestSPIFFEIDTemplateKeepsUpstreamPerilinkleFormat(t *testing.T) {
	tmpl, err := NewSPIFFEIDTemplate("{trustDomain}/{namespace}/sandbox/{sandboxID}")
	if err != nil {
		t.Fatalf("NewSPIFFEIDTemplate: %v", err)
	}
	if got, want := tmpl.Render("spiffe://openshell.local", "tenant-a", "x", "sandbox-123"), "spiffe://openshell.local/tenant-a/sandbox/sandbox-123"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestZeroValueSPIFFEIDTemplateRendersDefault(t *testing.T) {
	// An unset template must never yield an empty Keycloak client ID.
	var tmpl SPIFFEIDTemplate
	if got, want := tmpl.Render("spiffe://openshell.local", "ns", "n", "id-9"), "spiffe://openshell.local/openshell/sandbox/id-9"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestSPIFFEIDTemplateRequiresSandboxID(t *testing.T) {
	// Without the sandbox ID every sandbox would map to the same Keycloak client.
	if _, err := NewSPIFFEIDTemplate("{trustDomain}/ns/{namespace}"); err == nil {
		t.Fatal("expected error for template without {sandboxID}")
	}
}

func TestSPIFFEIDTemplateRejectsUnknownPlaceholder(t *testing.T) {
	if _, err := NewSPIFFEIDTemplate("{trustDomain}/{podName}/{sandboxID}"); err == nil {
		t.Fatal("expected error for unknown placeholder {podName}")
	}
}
