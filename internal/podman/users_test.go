package podman

import (
	"path/filepath"
	"testing"
)

func TestLoadUsersFromKubernetesUsersManifest(t *testing.T) {
	path := filepath.Join("..", "..", "config", "with-users", "users.yaml")

	users, err := LoadUsers(path)
	if err != nil {
		t.Fatalf("load users: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("users = %d, want 3", len(users))
	}

	var adminFound bool
	for _, user := range users {
		if user.Username != "admin-user" {
			continue
		}
		adminFound = true
		if user.Password != "admin123" {
			t.Fatalf("admin-user password = %q, want admin123", user.Password)
		}
		if !containsString(user.Roles, "openshell-admin") {
			t.Fatalf("admin-user roles = %#v, want openshell-admin", user.Roles)
		}
		if !user.Enabled {
			t.Fatalf("admin-user enabled = false, want true")
		}
	}
	if !adminFound {
		t.Fatalf("admin-user not loaded")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
