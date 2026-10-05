package controller

import (
	"context"
	"reflect"
	"testing"

	"github.com/gsim/perilinkle/api/v1alpha1"
	"github.com/gsim/perilinkle/internal/keycloak"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestProfileTargetDefaults(t *testing.T) {
	spec := v1alpha1.TargetService{Name: "alpha"}

	if got := profileTargetClientID(spec); got != "alpha" {
		t.Fatalf("client ID = %q, want %q", got, "alpha")
	}
	if got := profileTargetNamespace("profile-ns", spec); got != "profile-ns" {
		t.Fatalf("namespace = %q, want %q", got, "profile-ns")
	}
	if got := profileTargetServiceAccount(spec); got != "alpha" {
		t.Fatalf("service account = %q, want %q", got, "alpha")
	}
}

func TestProfileTargetSPIFFEIDDefault(t *testing.T) {
	spec := v1alpha1.TargetService{Name: "alpha", Namespace: "target-ns", ServiceAccountName: "alpha-sa"}

	got := profileTargetSPIFFESubject("spiffe://openshell.local", "profile-ns", spec)
	want := "spiffe://openshell.local/ns/target-ns/sa/alpha-sa"
	if got != want {
		t.Fatalf("SPIFFE subject = %q, want %q", got, want)
	}
}

func TestProfileTargetSPIFFEIDOverride(t *testing.T) {
	spec := v1alpha1.TargetService{Name: "alpha", SPIFFEID: "spiffe://example/custom/alpha"}

	got := profileTargetSPIFFESubject("spiffe://openshell.local", "profile-ns", spec)
	if got != spec.SPIFFEID {
		t.Fatalf("SPIFFE subject = %q, want %q", got, spec.SPIFFEID)
	}
}

func TestServiceGroupReconcileExistingSandboxes(t *testing.T) {
	managed := sandboxObject("managed", "sandbox-ns", map[string]string{
		LabelManaged:   "openshell",
		LabelSandboxID: "sandbox-1",
	})
	unmanaged := sandboxObject("unmanaged", "sandbox-ns", map[string]string{
		LabelManaged:   "other",
		LabelSandboxID: "sandbox-2",
	})
	missingID := sandboxObject("missing-id", "sandbox-ns", map[string]string{
		LabelManaged: "openshell",
	})
	keycloak := &recordingKeycloak{}
	upstreamTemplate, err := NewSPIFFEIDTemplate("{trustDomain}/{namespace}/sandbox/{sandboxID}")
	if err != nil {
		t.Fatalf("NewSPIFFEIDTemplate: %v", err)
	}
	reconciler := &ServiceGroupReconciler{
		Client: fake.NewClientBuilder().
			WithObjects(managed, unmanaged, missingID).
			Build(),
		Keycloak: keycloak,
		Config: ServiceGroupConfig{
			ManagedLabel:      LabelManaged,
			ManagedLabelValue: "openshell",
			SPIFFETrustDomain: "spiffe://openshell.local",
			SandboxGVK:        v1beta1SandboxGVK,
			SPIFFEIDTemplate:  upstreamTemplate,
		},
	}

	if err := reconciler.reconcileExistingSandboxes(context.Background(), keycloakRealm("openshell")); err != nil {
		t.Fatalf("reconcile existing sandboxes: %v", err)
	}

	if len(keycloak.sandboxes) != 1 {
		t.Fatalf("ensured sandboxes = %d, want 1", len(keycloak.sandboxes))
	}
	got := keycloak.sandboxes[0]
	if got.ID != "sandbox-1" {
		t.Fatalf("sandbox ID = %q, want sandbox-1", got.ID)
	}
	if got.Namespace != "sandbox-ns" {
		t.Fatalf("sandbox namespace = %q, want sandbox-ns", got.Namespace)
	}
	wantSubject := "spiffe://openshell.local/sandbox-ns/sandbox/sandbox-1"
	if got.SPIFFESubject != wantSubject {
		t.Fatalf("sandbox SPIFFE subject = %q, want %q", got.SPIFFESubject, wantSubject)
	}
}

func TestOpenShellProviderProfileBuilder(t *testing.T) {
	group := &v1alpha1.ServiceGroup{}
	group.Name = "dummy-services"
	group.Namespace = "openshell"
	group.Spec = v1alpha1.ServiceGroupSpec{
		ProviderProfile: v1alpha1.ProviderProfileSpec{
			Enabled:               true,
			ID:                    "keycloak-exchange",
			DisplayName:           "Keycloak protected services with on-behalf-of token semantics",
			Description:           "Token exchange for Keycloak-protected services using SPIFFE authentication",
			Category:              "other",
			JWTSVIDAudience:       "http://keycloak.192.168.39.75.sslip.io/realms/openshell",
			EndpointDefaults:      v1alpha1.ProviderProfileEndpointDefaults{Port: 80, Protocol: "rest", TLS: "none", Access: "read-write"},
			SubjectCredentialName: "subject_token",
			AccessCredentialName:  "access_token",
			ClientAssertionType:   "urn:ietf:params:oauth:client-assertion-type:jwt-spiffe",
			Binaries: []v1alpha1.ProviderProfileBinary{
				{Path: "/usr/bin/curl"},
				{Path: "/usr/bin/wget"},
			},
		},
		TargetServices: []v1alpha1.TargetService{
			{Name: "alpha", Namespace: "default", ServiceAccountName: "alpha"},
			{
				Name:      "beta",
				Namespace: "default",
				Endpoint:  v1alpha1.TargetServiceEndpoint{Host: "beta-api.default.svc.cluster.local", Port: 8080, Access: "read-only", AllowedIPs: []string{"10.96.0.10/32"}},
				Token:     v1alpha1.TargetServiceToken{Audience: "beta-api", Scopes: []string{"beta.read"}},
			},
		},
	}
	reconciler := &ServiceGroupReconciler{
		Config: ServiceGroupConfig{
			KeycloakURL: "http://keycloak.openshell.svc.cluster.local",
			Realm:       "openshell",
		},
	}

	profile, err := reconciler.openShellProviderProfile(group, keycloakRealm("openshell"))
	if err != nil {
		t.Fatalf("build openshell provider profile: %v", err)
	}

	if profile.ID != "keycloak-exchange" {
		t.Fatalf("profile ID = %q, want keycloak-exchange", profile.ID)
	}
	if got := profile.Credentials[1].TokenGrant.TokenEndpoint; got != "http://keycloak.openshell.svc.cluster.local/realms/openshell/protocol/openid-connect/token" {
		t.Fatalf("token endpoint = %q", got)
	}
	if got := profile.Credentials[1].TokenGrant.JWTSVIDAudience; got != "http://keycloak.192.168.39.75.sslip.io/realms/openshell" {
		t.Fatalf("jwt svid audience = %q", got)
	}
	if len(profile.Endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2", len(profile.Endpoints))
	}
	if profile.Endpoints[0].Host != "alpha.default.svc.cluster.local" {
		t.Fatalf("alpha host = %q", profile.Endpoints[0].Host)
	}
	if profile.Endpoints[1].Host != "beta-api.default.svc.cluster.local" {
		t.Fatalf("beta host = %q", profile.Endpoints[1].Host)
	}
	if profile.Endpoints[1].Port != 8080 {
		t.Fatalf("beta port = %d, want 8080", profile.Endpoints[1].Port)
	}
	if !reflect.DeepEqual(profile.Endpoints[1].AllowedIPs, []string{"10.96.0.10/32"}) {
		t.Fatalf("beta allowed IPs = %#v", profile.Endpoints[1].AllowedIPs)
	}
	overrides := profile.Credentials[1].TokenGrant.AudienceOverrideList
	if len(overrides) != 2 {
		t.Fatalf("audience overrides = %d, want 2", len(overrides))
	}
	if overrides[0].Audience != "alpha" {
		t.Fatalf("alpha audience = %q, want alpha", overrides[0].Audience)
	}
	if overrides[1].Audience != "beta-api" {
		t.Fatalf("beta audience = %q, want beta-api", overrides[1].Audience)
	}
	if !reflect.DeepEqual(overrides[1].Scopes, []string{"beta.read"}) {
		t.Fatalf("beta scopes = %#v", overrides[1].Scopes)
	}
}

func TestOpenShellGatewayDescriptorSupportsProviderProfile(t *testing.T) {
	types, err := newOpenShellProtoTypes()
	if err != nil {
		t.Fatalf("build openshell proto types: %v", err)
	}
	client := &openShellGatewayClient{types: types}
	msg := client.providerProfileMessage(&openShellProviderProfile{
		ID:          "keycloak-exchange",
		DisplayName: "Keycloak Exchange",
		Category:    "other",
		Credentials: []openShellProviderCredential{
			{Name: "subject_token"},
			{
				Name:       "access_token",
				AuthStyle:  "bearer",
				HeaderName: "Authorization",
				TokenGrant: &openShellTokenGrant{
					GrantType:            "token_exchange",
					TokenEndpoint:        "http://keycloak/realms/openshell/protocol/openid-connect/token",
					ClientAssertionType:  defaultClientAssertionType,
					JWTSVIDAudience:      "http://keycloak/realms/openshell",
					SubjectToken:         &openShellSubjectToken{Source: "provider_credential", Credential: "subject_token"},
					AudienceOverrideList: []openShellAudienceOverride{{Host: "alpha.default.svc.cluster.local", Port: 80, Audience: "alpha"}},
				},
			},
		},
		Endpoints: []openShellEndpoint{{Host: "alpha.default.svc.cluster.local", Port: 80, Protocol: "rest", TLS: "none", Access: "read-write"}},
		Binaries:  []openShellBinary{{Path: "/usr/bin/curl"}},
	})

	if got := msg.Get(types.providerProfile.Fields().ByName("id")).String(); got != "keycloak-exchange" {
		t.Fatalf("dynamic profile id = %q", got)
	}
	if got := msg.Get(types.providerProfile.Fields().ByName("endpoints")).List().Len(); got != 1 {
		t.Fatalf("dynamic profile endpoints = %d, want 1", got)
	}
}

func sandboxObject(name, namespace string, labels map[string]string) *unstructured.Unstructured {
	obj := agentSandboxObject(v1beta1SandboxGVK)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetLabels(labels)
	return obj
}

func keycloakRealm(name string) keycloak.Realm {
	return keycloak.Realm{Name: name}
}

type recordingKeycloak struct {
	sandboxes []keycloak.Sandbox
}

func (r *recordingKeycloak) EnsureRealm(context.Context, keycloak.Realm) error {
	return nil
}

func (r *recordingKeycloak) EnsureTargetService(context.Context, keycloak.Realm, keycloak.TargetService) error {
	return nil
}

func (r *recordingKeycloak) EnsureSandbox(_ context.Context, _ keycloak.Realm, sandbox keycloak.Sandbox) error {
	r.sandboxes = append(r.sandboxes, sandbox)
	return nil
}

func (r *recordingKeycloak) DeleteSandbox(context.Context, keycloak.Realm, keycloak.Sandbox) error {
	return nil
}

func (r *recordingKeycloak) EnsureUsers(context.Context, keycloak.Realm, []keycloak.User) error {
	return nil
}

var _ keycloak.Reconciler = (*recordingKeycloak)(nil)
