package keycloak

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// tokenServer records the admin token request form and returns a token or an error status.
func tokenServer(t *testing.T, status int) (*httptest.Server, *url.Values, *string) {
	t.Helper()
	form := &url.Values{}
	path := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		*form = r.PostForm
		*path = r.URL.Path
		if status != http.StatusOK {
			http.Error(w, `{"error":"unauthorized_client"}`, status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":300}`))
	}))
	t.Cleanup(srv.Close)
	return srv, form, path
}

func TestAdminTokenUsesClientCredentialsWhenConfigured(t *testing.T) {
	srv, form, path := tokenServer(t, http.StatusOK)
	c := NewClient(Config{
		BaseURL:           srv.URL,
		AdminRealm:        "openshell",
		AdminClientID:     "perilinkle",
		AdminClientSecret: "s3cret",
		AdminUsername:     "admin",
		AdminPassword:     "should-not-be-sent",
	})

	if err := c.ensureToken(context.Background()); err != nil {
		t.Fatalf("ensureToken: %v", err)
	}
	if got := form.Get("grant_type"); got != "client_credentials" {
		t.Fatalf("grant_type = %q, want client_credentials", got)
	}
	if form.Get("client_id") != "perilinkle" || form.Get("client_secret") != "s3cret" {
		t.Fatalf("client credentials not sent: %v", *form)
	}
	if form.Has("username") || form.Has("password") {
		t.Fatalf("admin username/password leaked into client_credentials request: %v", *form)
	}
	if *path != "/realms/openshell/protocol/openid-connect/token" {
		t.Fatalf("token path = %q, want admin realm token endpoint", *path)
	}
	if c.accessToken != "tok" {
		t.Fatalf("accessToken = %q, want tok", c.accessToken)
	}
}

func TestAdminTokenFallsBackToPasswordGrant(t *testing.T) {
	srv, form, _ := tokenServer(t, http.StatusOK)
	c := NewClient(Config{BaseURL: srv.URL, AdminRealm: "master", AdminUsername: "admin", AdminPassword: "pw"})

	if err := c.ensureToken(context.Background()); err != nil {
		t.Fatalf("ensureToken: %v", err)
	}
	if form.Get("grant_type") != "password" || form.Get("client_id") != "admin-cli" || form.Get("username") != "admin" {
		t.Fatalf("unexpected password grant form: %v", *form)
	}
}

func TestAdminTokenReportsKeycloakError(t *testing.T) {
	srv, _, _ := tokenServer(t, http.StatusUnauthorized)
	c := NewClient(Config{BaseURL: srv.URL, AdminRealm: "openshell", AdminClientID: "perilinkle", AdminClientSecret: "wrong"})

	err := c.ensureToken(context.Background())
	if err == nil || !strings.Contains(err.Error(), "status=401") {
		t.Fatalf("err = %v, want admin token failure with status=401", err)
	}
	if c.accessToken != "" {
		t.Fatal("token cached after failed request")
	}
}
