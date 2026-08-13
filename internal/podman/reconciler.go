package podman

import (
	"context"
	"fmt"
	"strings"

	"github.com/gsim/perilinkle/api/v1alpha1"
	"github.com/gsim/perilinkle/internal/keycloak"
)

func (r *Reconciler) Reconcile(ctx context.Context, groups []v1alpha1.ServiceGroup) error {
	if r.Runtime == nil {
		return fmt.Errorf("podman runtime is not configured")
	}
	if r.Keycloak == nil {
		return fmt.Errorf("keycloak reconciler is not configured")
	}
	config := NormalizeConfig(r.Config)
	realm := keycloak.Realm{Name: config.Realm}

	containers, err := r.Runtime.ListContainers(ctx)
	if err != nil {
		return err
	}
	gateway, err := gatewayContainer(containers, config)
	if err != nil {
		return err
	}
	gatewaySPIFFEID := config.GatewaySPIFFEID
	if gatewaySPIFFEID == "" {
		gatewaySPIFFEID = gatewaySPIFFESubject(config, gateway)
	}

	if err := r.Keycloak.EnsureRealm(ctx, realm); err != nil {
		return fmt.Errorf("ensure keycloak realm: %w", err)
	}

	if r.SPIRE != nil {
		keycloak, err := keycloakContainer(containers, config)
		if err != nil {
			return err
		}
		keycloakSPIFFEID := config.KeycloakSPIFFEID
		if keycloakSPIFFEID == "" {
			keycloakSPIFFEID = keycloakSPIFFESubject(config, keycloak)
		}
		keycloakSelectors, err := keycloakSelectors(config, keycloak)
		if err != nil {
			return err
		}
		if err := r.SPIRE.EnsureEntry(ctx, SPIREEntry{
			SPIFFEID:  keycloakSPIFFEID,
			ParentID:  config.SPIREParentID,
			Selectors: keycloakSelectors,
		}); err != nil {
			return fmt.Errorf("ensure keycloak spire entry: %w", err)
		}
		if err := r.SPIRE.EnsureEntry(ctx, SPIREEntry{
			SPIFFEID:  gatewaySPIFFEID,
			ParentID:  config.SPIREParentID,
			Selectors: gatewaySelectors(config, gateway),
		}); err != nil {
			return fmt.Errorf("ensure gateway spire entry: %w", err)
		}
	}

	for _, group := range groups {
		for _, spec := range group.Spec.TargetServices {
			target := keycloak.TargetService{
				ClientID:           targetClientID(spec),
				Namespace:          targetNamespace(group.Namespace, spec),
				ServiceName:        spec.Name,
				ServiceAccountName: targetServiceAccount(spec),
				SPIFFESubject:      targetSPIFFESubject(config.SPIFFETrustDomain, group.Namespace, spec),
			}
			if err := r.Keycloak.EnsureTargetService(ctx, realm, target); err != nil {
				return fmt.Errorf("ensure target service %q: %w", spec.Name, err)
			}
		}
	}

	for _, container := range sandboxContainers(containers, config) {
		sandbox := sandboxForContainer(config, container)
		if err := r.Keycloak.EnsureSandbox(ctx, realm, sandbox); err != nil {
			return fmt.Errorf("ensure sandbox %q: %w", sandbox.ID, err)
		}
		if r.SPIRE != nil {
			if err := r.SPIRE.EnsureEntry(ctx, SPIREEntry{
				SPIFFEID:  sandbox.SPIFFESubject,
				ParentID:  config.SPIREParentID,
				Selectors: sandboxSelectors(config, container),
			}); err != nil {
				return fmt.Errorf("ensure sandbox %q spire entry: %w", sandbox.ID, err)
			}
		}
	}

	return nil
}

func (r *Reconciler) ReconcileKeycloak(ctx context.Context, groups []v1alpha1.ServiceGroup) error {
	if r.Keycloak == nil {
		return fmt.Errorf("keycloak reconciler is not configured")
	}
	config := NormalizeConfig(r.Config)
	realm := keycloak.Realm{Name: config.Realm}
	if err := r.Keycloak.EnsureRealm(ctx, realm); err != nil {
		return fmt.Errorf("ensure keycloak realm: %w", err)
	}
	for _, group := range groups {
		for _, spec := range group.Spec.TargetServices {
			target := keycloak.TargetService{
				ClientID:           targetClientID(spec),
				Namespace:          targetNamespace(group.Namespace, spec),
				ServiceName:        spec.Name,
				ServiceAccountName: targetServiceAccount(spec),
				SPIFFESubject:      targetSPIFFESubject(config.SPIFFETrustDomain, group.Namespace, spec),
			}
			if err := r.Keycloak.EnsureTargetService(ctx, realm, target); err != nil {
				return fmt.Errorf("ensure target service %q: %w", spec.Name, err)
			}
		}
	}
	return nil
}

func NormalizeConfig(override Config) Config {
	config := DefaultConfig()
	if override.ManagedLabel != "" {
		config.ManagedLabel = override.ManagedLabel
	}
	if override.ManagedLabelValue != "" {
		config.ManagedLabelValue = override.ManagedLabelValue
	}
	if override.SandboxIDLabel != "" {
		config.SandboxIDLabel = override.SandboxIDLabel
	}
	if override.GatewayIDLabel != "" {
		config.GatewayIDLabel = override.GatewayIDLabel
	}
	if override.RoleLabel != "" {
		config.RoleLabel = override.RoleLabel
	}
	if override.NamespaceLabel != "" {
		config.NamespaceLabel = override.NamespaceLabel
	}
	if override.Realm != "" {
		config.Realm = override.Realm
	}
	if override.SPIFFETrustDomain != "" {
		config.SPIFFETrustDomain = strings.TrimRight(override.SPIFFETrustDomain, "/")
	}
	if override.SandboxNamespace != "" {
		config.SandboxNamespace = override.SandboxNamespace
	}
	if override.KeycloakLabel != "" {
		config.KeycloakLabel = override.KeycloakLabel
	}
	if override.KeycloakLabelValue != "" {
		config.KeycloakLabelValue = override.KeycloakLabelValue
	}
	config.GatewaySPIFFEID = override.GatewaySPIFFEID
	config.GatewayContainer = override.GatewayContainer
	config.KeycloakSPIFFEID = override.KeycloakSPIFFEID
	config.KeycloakContainer = override.KeycloakContainer
	config.SPIREParentID = override.SPIREParentID
	return config
}

func GatewaySPIFFEID(containers []Container, config Config) (string, error) {
	config = NormalizeConfig(config)
	gateway, err := gatewayContainer(containers, config)
	if err != nil {
		return "", err
	}
	if config.GatewaySPIFFEID != "" {
		return config.GatewaySPIFFEID, nil
	}
	return gatewaySPIFFESubject(config, gateway), nil
}

func KeycloakSPIFFEID(containers []Container, config Config) (string, error) {
	config = NormalizeConfig(config)
	keycloak, err := keycloakContainer(containers, config)
	if err != nil {
		return "", err
	}
	if config.KeycloakSPIFFEID != "" {
		return config.KeycloakSPIFFEID, nil
	}
	return keycloakSPIFFESubject(config, keycloak), nil
}

func gatewayContainer(containers []Container, config Config) (Container, error) {
	if config.GatewayContainer != "" {
		for _, container := range containers {
			if container.ID == config.GatewayContainer || container.Name == config.GatewayContainer || strings.HasPrefix(container.ID, config.GatewayContainer) {
				return container, nil
			}
		}
		return Container{}, fmt.Errorf("gateway container %q not found", config.GatewayContainer)
	}
	for _, container := range containers {
		if container.Labels[config.ManagedLabel] == config.ManagedLabelValue && container.Labels[config.RoleLabel] == RoleGateway {
			return container, nil
		}
	}
	return Container{}, fmt.Errorf("gateway container not found; set %s=%s and %s=%s labels or pass --gateway-container", config.ManagedLabel, config.ManagedLabelValue, config.RoleLabel, RoleGateway)
}

func keycloakContainer(containers []Container, config Config) (Container, error) {
	if config.KeycloakContainer != "" {
		for _, container := range containers {
			if container.ID == config.KeycloakContainer || container.Name == config.KeycloakContainer || strings.HasPrefix(container.ID, config.KeycloakContainer) {
				return container, nil
			}
		}
		return Container{}, fmt.Errorf("keycloak container %q not found", config.KeycloakContainer)
	}
	for _, container := range containers {
		if container.Labels[config.KeycloakLabel] == config.KeycloakLabelValue {
			return container, nil
		}
	}
	return Container{}, fmt.Errorf("keycloak container not found; set %s=%s label or pass --keycloak-container", config.KeycloakLabel, config.KeycloakLabelValue)
}

func sandboxContainers(containers []Container, config Config) []Container {
	var sandboxes []Container
	for _, container := range containers {
		if container.Labels[config.ManagedLabel] != config.ManagedLabelValue {
			continue
		}
		if container.Labels[config.SandboxIDLabel] == "" {
			continue
		}
		sandboxes = append(sandboxes, container)
	}
	return sandboxes
}

func sandboxForContainer(config Config, container Container) keycloak.Sandbox {
	namespace := container.Labels[config.NamespaceLabel]
	if namespace == "" {
		namespace = config.SandboxNamespace
	}
	id := container.Labels[config.SandboxIDLabel]
	return keycloak.Sandbox{
		ID:            id,
		Namespace:     namespace,
		Name:          container.Name,
		SPIFFESubject: sandboxSPIFFESubject(config.SPIFFETrustDomain, namespace, id),
	}
}

func sandboxSPIFFESubject(trustDomain, namespace, sandboxID string) string {
	return strings.TrimRight(trustDomain, "/") + "/" + namespace + "/sandbox/" + sandboxID
}

func gatewaySPIFFESubject(config Config, container Container) string {
	id := container.Labels[config.GatewayIDLabel]
	if id == "" {
		id = firstNonEmpty(container.Name, container.ID)
	}
	return strings.TrimRight(config.SPIFFETrustDomain, "/") + "/podman/gateway/" + id
}

func keycloakSPIFFESubject(config Config, container Container) string {
	id := firstNonEmpty(container.Name, container.ID)
	return strings.TrimRight(config.SPIFFETrustDomain, "/") + "/podman/keycloak/" + id
}

func gatewaySelectors(config Config, container Container) []string {
	if value := container.Labels[config.GatewayIDLabel]; value != "" {
		return []string{
			dockerLabelSelector(config.ManagedLabel, config.ManagedLabelValue),
			dockerLabelSelector(config.GatewayIDLabel, value),
		}
	}
	return []string{
		dockerLabelSelector(config.ManagedLabel, config.ManagedLabelValue),
		dockerLabelSelector(config.RoleLabel, RoleGateway),
	}
}

func keycloakSelectors(config Config, container Container) ([]string, error) {
	value := container.Labels[config.KeycloakLabel]
	if value == "" {
		return nil, fmt.Errorf("keycloak container %q is missing %s label", container.Name, config.KeycloakLabel)
	}
	return []string{
		dockerLabelSelector(config.KeycloakLabel, value),
	}, nil
}

func sandboxSelectors(config Config, container Container) []string {
	return []string{
		dockerLabelSelector(config.ManagedLabel, config.ManagedLabelValue),
		dockerLabelSelector(config.SandboxIDLabel, container.Labels[config.SandboxIDLabel]),
	}
}

func dockerLabelSelector(name, value string) string {
	return "docker:label:" + name + ":" + value
}

func targetClientID(spec v1alpha1.TargetService) string {
	if spec.ClientID != "" {
		return spec.ClientID
	}
	return spec.Name
}

func targetNamespace(profileNamespace string, spec v1alpha1.TargetService) string {
	if spec.Namespace != "" {
		return spec.Namespace
	}
	return profileNamespace
}

func targetServiceAccount(spec v1alpha1.TargetService) string {
	if spec.ServiceAccountName != "" {
		return spec.ServiceAccountName
	}
	return spec.Name
}

func targetSPIFFESubject(trustDomain, profileNamespace string, spec v1alpha1.TargetService) string {
	if spec.SPIFFEID != "" {
		return spec.SPIFFEID
	}
	return strings.TrimRight(trustDomain, "/") + "/ns/" + targetNamespace(profileNamespace, spec) + "/sa/" + targetServiceAccount(spec)
}
