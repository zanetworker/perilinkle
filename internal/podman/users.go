package podman

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/gsim/perilinkle/internal/keycloak"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const usersConfigKey = "users.json"

func LoadUsers(path string) ([]keycloak.User, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read users file %q: %w", path, err)
	}

	var rawUsers string
	passwords := map[string]string{}
	for _, doc := range yamlDocuments(string(data)) {
		var meta metav1.TypeMeta
		if err := yaml.Unmarshal([]byte(doc), &meta); err != nil {
			return nil, fmt.Errorf("parse users file %q document type: %w", path, err)
		}
		switch meta.Kind {
		case "ConfigMap":
			var configMap corev1.ConfigMap
			if err := yaml.Unmarshal([]byte(doc), &configMap); err != nil {
				return nil, fmt.Errorf("parse users configmap in %q: %w", path, err)
			}
			if configMap.Data[usersConfigKey] != "" {
				rawUsers = configMap.Data[usersConfigKey]
			}
		case "Secret":
			var secret corev1.Secret
			if err := yaml.Unmarshal([]byte(doc), &secret); err != nil {
				return nil, fmt.Errorf("parse users secret in %q: %w", path, err)
			}
			for key, value := range secret.Data {
				passwords[key] = string(value)
			}
			for key, value := range secret.StringData {
				passwords[key] = value
			}
		}
	}
	if strings.TrimSpace(rawUsers) == "" {
		return nil, nil
	}

	var config usersConfig
	if err := json.Unmarshal([]byte(rawUsers), &config); err != nil {
		return nil, fmt.Errorf("parse %s in %q: %w", usersConfigKey, path, err)
	}
	users := make([]keycloak.User, 0, len(config.Users))
	for _, configured := range config.Users {
		passwordKey := configured.PasswordKey
		if passwordKey == "" {
			passwordKey = configured.Username
		}
		enabled := true
		if configured.Enabled != nil {
			enabled = *configured.Enabled
		}
		users = append(users, keycloak.User{
			Username:      configured.Username,
			FirstName:     configured.FirstName,
			LastName:      configured.LastName,
			Email:         configured.Email,
			EmailVerified: configured.EmailVerified,
			Enabled:       enabled,
			Roles:         configured.Roles,
			Password:      passwords[passwordKey],
		})
	}
	return users, nil
}

func yamlDocuments(data string) []string {
	var docs []string
	for _, doc := range strings.Split(data, "\n---") {
		doc = strings.TrimSpace(doc)
		if doc != "" {
			docs = append(docs, doc)
		}
	}
	return docs
}

type usersConfig struct {
	Users []configuredUser `json:"users"`
}

type configuredUser struct {
	Username      string   `json:"username"`
	FirstName     string   `json:"firstName"`
	LastName      string   `json:"lastName"`
	Email         string   `json:"email"`
	EmailVerified bool     `json:"emailVerified"`
	Enabled       *bool    `json:"enabled"`
	Roles         []string `json:"roles"`
	PasswordKey   string   `json:"passwordKey"`
}
