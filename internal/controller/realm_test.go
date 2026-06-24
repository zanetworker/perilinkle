package controller

import "testing"

func TestConfiguredRealmDefaultsToOpenShell(t *testing.T) {
	realm := configuredRealm("")
	if realm.Name != "openshell" {
		t.Fatalf("realm name = %q, want %q", realm.Name, "openshell")
	}
}

func TestConfiguredRealmUsesConfiguredName(t *testing.T) {
	realm := configuredRealm("custom")
	if realm.Name != "custom" {
		t.Fatalf("realm name = %q, want %q", realm.Name, "custom")
	}
}
