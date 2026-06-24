package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/gsim/perilinkle/internal/keycloak"
	ctrl "sigs.k8s.io/controller-runtime"
)

type RealmBootstrapper struct {
	Keycloak keycloak.Reconciler
	Realm    string
	Interval time.Duration
}

func (b *RealmBootstrapper) Start(ctx context.Context) error {
	if b.Interval == 0 {
		b.Interval = 15 * time.Second
	}
	reconciled := false
	if err := b.reconcile(ctx); err != nil {
		ctrl.LoggerFrom(ctx).Info("realm bootstrap waiting for Keycloak", "error", err.Error())
	} else {
		reconciled = true
		ctrl.LoggerFrom(ctx).Info("realm bootstrap reconciled")
	}
	ticker := time.NewTicker(b.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := b.reconcile(ctx); err != nil {
				if reconciled {
					ctrl.LoggerFrom(ctx).Error(err, "realm bootstrap reconcile failed")
				} else {
					ctrl.LoggerFrom(ctx).Info("realm bootstrap waiting for Keycloak", "error", err.Error())
				}
				continue
			}
			if !reconciled {
				ctrl.LoggerFrom(ctx).Info("realm bootstrap reconciled")
			}
			reconciled = true
		}
	}
}

func (b *RealmBootstrapper) reconcile(ctx context.Context) error {
	realm := configuredRealm(b.Realm)
	if err := b.Keycloak.EnsureRealm(ctx, realm); err != nil {
		return fmt.Errorf("ensure configured realm %q: %w", realm.Name, err)
	}
	return nil
}
