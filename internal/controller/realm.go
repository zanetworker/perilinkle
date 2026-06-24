package controller

import "github.com/gsim/perilinkle/internal/keycloak"

const DefaultRealm = "openshell"

func configuredRealm(name string) keycloak.Realm {
	if name == "" {
		name = DefaultRealm
	}
	return keycloak.Realm{Name: name}
}
