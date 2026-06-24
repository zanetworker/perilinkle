package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gsim/perilinkle/internal/keycloak"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const usersConfigMapKey = "users.json"

type UserBootstrapper struct {
	Client   client.Client
	Keycloak keycloak.Reconciler
	Config   UserBootstrapConfig
}

type UserBootstrapConfig struct {
	Namespace     string
	ConfigMapName string
	SecretName    string
	Realm         string
	Interval      time.Duration
}

func (b *UserBootstrapper) Start(ctx context.Context) error {
	if b.Config.ConfigMapName == "" {
		<-ctx.Done()
		return nil
	}
	if b.Config.Interval == 0 {
		b.Config.Interval = time.Minute
	}
	if err := b.reconcile(ctx); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "user bootstrap reconcile failed")
	}
	ticker := time.NewTicker(b.Config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := b.reconcile(ctx); err != nil {
				ctrl.LoggerFrom(ctx).Error(err, "user bootstrap reconcile failed")
			}
		}
	}
}

func (b *UserBootstrapper) reconcile(ctx context.Context) error {
	users, err := b.loadUsers(ctx)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	realm := configuredRealm(b.Config.Realm)
	if err := b.Keycloak.EnsureUsers(ctx, realm, users); err != nil {
		return fmt.Errorf("ensure users in realm %q: %w", realm.Name, err)
	}
	return nil
}

func (b *UserBootstrapper) loadUsers(ctx context.Context) ([]keycloak.User, error) {
	var configMap corev1.ConfigMap
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Config.Namespace, Name: b.Config.ConfigMapName}, &configMap); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get users configmap: %w", err)
	}
	raw := configMap.Data[usersConfigMapKey]
	if raw == "" {
		return nil, nil
	}
	var config usersConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, fmt.Errorf("parse %s: %w", usersConfigMapKey, err)
	}

	passwords := map[string]string{}
	if b.Config.SecretName != "" {
		var secret corev1.Secret
		if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Config.Namespace, Name: b.Config.SecretName}, &secret); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("get users secret: %w", err)
			}
		} else {
			for key, value := range secret.Data {
				passwords[key] = string(value)
			}
		}
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
