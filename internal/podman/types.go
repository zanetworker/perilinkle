package podman

import (
	"context"

	"github.com/gsim/perilinkle/internal/keycloak"
)

const (
	LabelManaged     = "openshell.managed"
	LabelSandboxID   = "openshell.ai/sandbox-id"
	LabelGatewayID   = "openshell.ai/gateway-id"
	LabelRole        = "openshell.ai/role"
	LabelNamespace   = "openshell.ai/namespace"
	LabelKeycloakApp = "app"
	RoleGateway      = "gateway"
	ManagedLabelOpen = "true"
	KeycloakAppValue = "keycloak"
)

type Container struct {
	ID     string
	Name   string
	Labels map[string]string
}

type Runtime interface {
	ListContainers(ctx context.Context) ([]Container, error)
}

type SPIRERegistrar interface {
	EnsureEntry(ctx context.Context, entry SPIREEntry) error
}

type SPIREEntry struct {
	SPIFFEID  string
	ParentID  string
	Selectors []string
}

type Config struct {
	ManagedLabel      string
	ManagedLabelValue string
	SandboxIDLabel    string
	GatewayIDLabel    string
	RoleLabel         string
	NamespaceLabel    string

	Realm              string
	SPIFFETrustDomain  string
	SandboxNamespace   string
	GatewaySPIFFEID    string
	GatewayContainer   string
	KeycloakSPIFFEID   string
	KeycloakContainer  string
	KeycloakLabel      string
	KeycloakLabelValue string
	SPIREParentID      string
}

type Reconciler struct {
	Runtime  Runtime
	Keycloak keycloak.Reconciler
	SPIRE    SPIRERegistrar
	Config   Config
}

func DefaultConfig() Config {
	return Config{
		ManagedLabel:       LabelManaged,
		ManagedLabelValue:  ManagedLabelOpen,
		SandboxIDLabel:     LabelSandboxID,
		GatewayIDLabel:     LabelGatewayID,
		RoleLabel:          LabelRole,
		NamespaceLabel:     LabelNamespace,
		Realm:              "openshell",
		SPIFFETrustDomain:  "spiffe://openshell.local",
		SandboxNamespace:   "podman",
		KeycloakLabel:      LabelKeycloakApp,
		KeycloakLabelValue: KeycloakAppValue,
	}
}
