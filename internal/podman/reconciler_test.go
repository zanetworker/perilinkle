package podman

import (
	"context"
	"reflect"
	"testing"

	"github.com/gsim/perilinkle/api/v1alpha1"
	"github.com/gsim/perilinkle/internal/keycloak"
)

func TestParsePodmanPS(t *testing.T) {
	data := []byte(`[
		{"Id":"abcdef123456","Names":["/openshell-gateway"],"Labels":{"openshell.managed":"true","openshell.ai/role":"gateway"}},
		{"ID":"123456abcdef","Names":"sandbox-one","Labels":{"openshell.managed":"true","openshell.ai/sandbox-id":"sandbox-1"}}
	]`)

	containers, err := ParsePodmanPS(data)
	if err != nil {
		t.Fatalf("parse podman ps: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("containers = %d, want 2", len(containers))
	}
	if containers[0].ID != "abcdef123456" {
		t.Fatalf("container[0].ID = %q", containers[0].ID)
	}
	if containers[0].Name != "openshell-gateway" {
		t.Fatalf("container[0].Name = %q", containers[0].Name)
	}
	if containers[1].Name != "sandbox-one" {
		t.Fatalf("container[1].Name = %q", containers[1].Name)
	}
}

func TestReconcileConfiguresKeycloakAndSPIRE(t *testing.T) {
	runtime := fakeRuntime{containers: []Container{
		{
			ID:   "gateway-container",
			Name: "openshell-gateway",
			Labels: map[string]string{
				LabelManaged:   ManagedLabelOpen,
				LabelRole:      RoleGateway,
				LabelGatewayID: "dev-gateway",
			},
		},
		{
			ID:   "keycloak-container",
			Name: "perilinkle-keycloak",
			Labels: map[string]string{
				LabelKeycloakApp: KeycloakAppValue,
			},
		},
		{
			ID:   "sandbox-container",
			Name: "sandbox-one",
			Labels: map[string]string{
				LabelManaged:   ManagedLabelOpen,
				LabelSandboxID: "sandbox-1",
				LabelNamespace: "tenant-a",
			},
		},
	}}
	keycloakRecorder := &recordingKeycloak{}
	spireRecorder := &recordingSPIRE{}
	group := v1alpha1.ServiceGroup{}
	group.Name = "services"
	group.Namespace = "services-ns"
	group.Spec.TargetServices = []v1alpha1.TargetService{
		{Name: "alpha", Namespace: "apps", ServiceAccountName: "alpha-sa"},
	}
	reconciler := &Reconciler{
		Runtime:  runtime,
		Keycloak: keycloakRecorder,
		SPIRE:    spireRecorder,
		Config: Config{
			Realm:             "openshell",
			SPIFFETrustDomain: "spiffe://openshell.local",
			SPIREParentID:     "spiffe://openshell.local/spire/agent/join_token/local",
		},
	}

	if err := reconciler.Reconcile(context.Background(), []v1alpha1.ServiceGroup{group}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(keycloakRecorder.realms) != 3 {
		t.Fatalf("ensure realm calls = %d, want 3", len(keycloakRecorder.realms))
	}
	if len(keycloakRecorder.targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(keycloakRecorder.targets))
	}
	target := keycloakRecorder.targets[0]
	if target.ClientID != "alpha" {
		t.Fatalf("target client ID = %q", target.ClientID)
	}
	if target.SPIFFESubject != "spiffe://openshell.local/ns/apps/sa/alpha-sa" {
		t.Fatalf("target SPIFFE subject = %q", target.SPIFFESubject)
	}
	if len(keycloakRecorder.sandboxes) != 1 {
		t.Fatalf("sandboxes = %d, want 1", len(keycloakRecorder.sandboxes))
	}
	sandbox := keycloakRecorder.sandboxes[0]
	if sandbox.SPIFFESubject != "spiffe://openshell.local/tenant-a/sandbox/sandbox-1" {
		t.Fatalf("sandbox SPIFFE subject = %q", sandbox.SPIFFESubject)
	}

	wantEntries := []SPIREEntry{
		{
			SPIFFEID: "spiffe://openshell.local/podman/keycloak/perilinkle-keycloak",
			ParentID: "spiffe://openshell.local/spire/agent/join_token/local",
			Selectors: []string{
				"docker:label:app:keycloak",
			},
		},
		{
			SPIFFEID: "spiffe://openshell.local/podman/gateway/dev-gateway",
			ParentID: "spiffe://openshell.local/spire/agent/join_token/local",
			Selectors: []string{
				"docker:label:openshell.managed:true",
				"docker:label:openshell.ai/gateway-id:dev-gateway",
			},
		},
		{
			SPIFFEID: "spiffe://openshell.local/tenant-a/sandbox/sandbox-1",
			ParentID: "spiffe://openshell.local/spire/agent/join_token/local",
			Selectors: []string{
				"docker:label:openshell.managed:true",
				"docker:label:openshell.ai/sandbox-id:sandbox-1",
			},
		},
	}
	if !reflect.DeepEqual(spireRecorder.entries, wantEntries) {
		t.Fatalf("spire entries = %#v, want %#v", spireRecorder.entries, wantEntries)
	}
}

func TestReconcileRequiresGateway(t *testing.T) {
	reconciler := &Reconciler{
		Runtime:  fakeRuntime{},
		Keycloak: &recordingKeycloak{},
	}

	if err := reconciler.Reconcile(context.Background(), nil); err == nil {
		t.Fatalf("reconcile error = nil, want missing gateway")
	}
}

type fakeRuntime struct {
	containers []Container
}

func (f fakeRuntime) ListContainers(context.Context) ([]Container, error) {
	return f.containers, nil
}

type recordingSPIRE struct {
	entries []SPIREEntry
}

func (r *recordingSPIRE) EnsureEntry(_ context.Context, entry SPIREEntry) error {
	r.entries = append(r.entries, entry)
	return nil
}

type recordingKeycloak struct {
	realms    []keycloak.Realm
	targets   []keycloak.TargetService
	sandboxes []keycloak.Sandbox
}

func (r *recordingKeycloak) EnsureRealm(_ context.Context, realm keycloak.Realm) error {
	r.realms = append(r.realms, realm)
	return nil
}

func (r *recordingKeycloak) EnsureTargetService(_ context.Context, _ keycloak.Realm, target keycloak.TargetService) error {
	r.realms = append(r.realms, keycloak.Realm{Name: "from-target"})
	r.targets = append(r.targets, target)
	return nil
}

func (r *recordingKeycloak) EnsureSandbox(_ context.Context, _ keycloak.Realm, sandbox keycloak.Sandbox) error {
	r.realms = append(r.realms, keycloak.Realm{Name: "from-sandbox"})
	r.sandboxes = append(r.sandboxes, sandbox)
	return nil
}

func (r *recordingKeycloak) DeleteSandbox(context.Context, keycloak.Realm, keycloak.Sandbox) error {
	return nil
}

func (r *recordingKeycloak) EnsureUsers(context.Context, keycloak.Realm, []keycloak.User) error {
	return nil
}
