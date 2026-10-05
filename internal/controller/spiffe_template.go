package controller

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultSPIFFEIDTemplate matches the per-sandbox ClusterSPIFFEID in the OpenShell
// Helm SPIRE overlay (deploy/helm/openshell/ci/values-spire-stack.yaml). The registered
// Keycloak client must equal the SVID subject exactly, so this must match the
// ClusterSPIFFEID used on the cluster.
const DefaultSPIFFEIDTemplate = "{trustDomain}/openshell/sandbox/{sandboxID}"

var spiffePlaceholder = regexp.MustCompile(`\{[^{}]*\}`)

var knownSPIFFEPlaceholders = map[string]bool{
	"{trustDomain}": true,
	"{namespace}":   true,
	"{name}":        true,
	"{sandboxID}":   true,
}

// SPIFFEIDTemplate renders the sandbox supervisor SPIFFE ID.
type SPIFFEIDTemplate struct {
	template string
}

func NewSPIFFEIDTemplate(template string) (SPIFFEIDTemplate, error) {
	if template == "" {
		template = DefaultSPIFFEIDTemplate
	}
	for _, placeholder := range spiffePlaceholder.FindAllString(template, -1) {
		if !knownSPIFFEPlaceholders[placeholder] {
			return SPIFFEIDTemplate{}, fmt.Errorf("SPIFFE ID template %q: unknown placeholder %s", template, placeholder)
		}
	}
	if !strings.Contains(template, "{sandboxID}") {
		return SPIFFEIDTemplate{}, fmt.Errorf("SPIFFE ID template %q must contain {sandboxID}", template)
	}
	return SPIFFEIDTemplate{template: template}, nil
}

func (t SPIFFEIDTemplate) Render(trustDomain, namespace, name, sandboxID string) string {
	template := t.template
	if template == "" {
		template = DefaultSPIFFEIDTemplate
	}
	return strings.NewReplacer(
		"{trustDomain}", strings.TrimSuffix(trustDomain, "/"),
		"{namespace}", namespace,
		"{name}", name,
		"{sandboxID}", sandboxID,
	).Replace(template)
}
