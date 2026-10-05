package controller

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/gsim/perilinkle/api/v1alpha1"
	"github.com/gsim/perilinkle/internal/keycloak"
)

const (
	defaultProviderProfileCategory               = "other"
	defaultSubjectCredentialName                 = "subject_token"
	defaultAccessCredentialName                  = "access_token"
	defaultClientAssertionType                   = "urn:ietf:params:oauth:client-assertion-type:jwt-spiffe"
	defaultProviderProfileEndpointPort     int32 = 80
	defaultProviderProfileEndpointProtocol       = "rest"
	defaultProviderProfileEndpointAccess         = "read-write"
)

func (r *ServiceGroupReconciler) openShellProviderProfile(group *v1alpha1.ServiceGroup, realm keycloak.Realm) (*openShellProviderProfile, error) {
	spec := group.Spec.ProviderProfile
	if !spec.Enabled {
		return nil, nil
	}

	profile := &openShellProviderProfile{
		ID:          providerProfileID(group),
		DisplayName: firstNonEmpty(spec.DisplayName, providerProfileID(group)),
		Description: spec.Description,
		Category:    firstNonEmpty(spec.Category, defaultProviderProfileCategory),
		Credentials: []openShellProviderCredential{
			{
				Name:        subjectCredentialName(spec),
				Description: "User token used as subject for exchange",
			},
			{
				Name:        accessCredentialName(spec),
				Description: "Access token obtained via OAuth2 token exchange",
				AuthStyle:   "bearer",
				HeaderName:  "Authorization",
				TokenGrant: &openShellTokenGrant{
					GrantType:            "token_exchange",
					TokenEndpoint:        firstNonEmpty(spec.TokenEndpoint, r.defaultTokenEndpoint(realm)),
					ClientAssertionType:  firstNonEmpty(spec.ClientAssertionType, defaultClientAssertionType),
					JWTSVIDAudience:      firstNonEmpty(spec.JWTSVIDAudience, r.defaultJWTSVIDAudience(realm)),
					SubjectToken:         &openShellSubjectToken{Source: "provider_credential", Credential: subjectCredentialName(spec)},
					AudienceOverrideList: []openShellAudienceOverride{},
				},
			},
		},
		Endpoints: []openShellEndpoint{},
		Binaries:  providerProfileBinaries(spec),
	}

	for _, target := range group.Spec.TargetServices {
		endpoint := openShellEndpointForTarget(group.Namespace, spec.EndpointDefaults, target)
		profile.Endpoints = append(profile.Endpoints, endpoint)
		profile.Credentials[1].TokenGrant.AudienceOverrideList = append(profile.Credentials[1].TokenGrant.AudienceOverrideList, openShellAudienceOverride{
			Host:     endpoint.Host,
			Port:     endpoint.Port,
			Audience: targetTokenAudience(target),
			Scopes:   copyStringSlice(target.Token.Scopes),
		})
	}

	if profile.ID == "" {
		return nil, fmt.Errorf("provider profile id is empty")
	}
	return profile, nil
}

func providerProfileID(group *v1alpha1.ServiceGroup) string {
	if group.Spec.ProviderProfile.ID != "" {
		return group.Spec.ProviderProfile.ID
	}
	return serviceGroupProviderProfileName(group)
}

func subjectCredentialName(spec v1alpha1.ProviderProfileSpec) string {
	return firstNonEmpty(spec.SubjectCredentialName, defaultSubjectCredentialName)
}

func accessCredentialName(spec v1alpha1.ProviderProfileSpec) string {
	return firstNonEmpty(spec.AccessCredentialName, defaultAccessCredentialName)
}

func providerProfileBinaries(spec v1alpha1.ProviderProfileSpec) []openShellBinary {
	if len(spec.Binaries) == 0 {
		return []openShellBinary{{Path: "/usr/bin/curl"}, {Path: "/usr/bin/wget"}}
	}
	binaries := make([]openShellBinary, 0, len(spec.Binaries))
	for _, binary := range spec.Binaries {
		if binary.Path == "" {
			continue
		}
		binaries = append(binaries, openShellBinary{Path: binary.Path})
	}
	return binaries
}

func openShellEndpointForTarget(profileNamespace string, defaults v1alpha1.ProviderProfileEndpointDefaults, target v1alpha1.TargetService) openShellEndpoint {
	namespace := profileTargetNamespace(profileNamespace, target)
	endpoint := openShellEndpoint{
		Host:       firstNonEmpty(target.Endpoint.Host, fmt.Sprintf("%s.%s.svc.cluster.local", target.Name, namespace)),
		Port:       firstNonZeroInt32(target.Endpoint.Port, defaults.Port, defaultProviderProfileEndpointPort),
		Protocol:   firstNonEmpty(target.Endpoint.Protocol, defaults.Protocol, defaultProviderProfileEndpointProtocol),
		TLS:        normalizeEndpointTLS(firstNonEmpty(target.Endpoint.TLS, defaults.TLS)),
		Access:     firstNonEmpty(target.Endpoint.Access, defaults.Access, defaultProviderProfileEndpointAccess),
		AllowedIPs: firstNonEmptyStringSlice(target.Endpoint.AllowedIPs, defaults.AllowedIPs),
	}
	return endpoint
}

// normalizeEndpointTLS maps the legacy "none" value, which current OpenShell rejects,
// to an omitted value (automatic TLS termination).
func normalizeEndpointTLS(value string) string {
	if value == "none" {
		return ""
	}
	return value
}

func targetTokenAudience(target v1alpha1.TargetService) string {
	if target.Token.Audience != "" {
		return target.Token.Audience
	}
	return profileTargetClientID(target)
}

func (r *ServiceGroupReconciler) defaultTokenEndpoint(realm keycloak.Realm) string {
	return strings.TrimRight(r.profileKeycloakBaseURL(), "/") + "/realms/" + url.PathEscape(realm.Name) + "/protocol/openid-connect/token"
}

func (r *ServiceGroupReconciler) defaultJWTSVIDAudience(realm keycloak.Realm) string {
	return strings.TrimRight(r.profileKeycloakBaseURL(), "/") + "/realms/" + url.PathEscape(realm.Name)
}

func (r *ServiceGroupReconciler) profileKeycloakBaseURL() string {
	return firstNonEmpty(r.Config.KeycloakPublicURL, r.Config.KeycloakURL)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonZeroInt32(values ...int32) int32 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonEmptyStringSlice(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return copyStringSlice(value)
		}
	}
	return nil
}

func copyStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

type openShellProviderProfile struct {
	ID          string                        `json:"id"`
	DisplayName string                        `json:"display_name,omitempty"`
	Description string                        `json:"description,omitempty"`
	Category    string                        `json:"category,omitempty"`
	Credentials []openShellProviderCredential `json:"credentials,omitempty"`
	Endpoints   []openShellEndpoint           `json:"endpoints,omitempty"`
	Binaries    []openShellBinary             `json:"binaries,omitempty"`
}

type openShellProviderCredential struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	AuthStyle   string               `json:"auth_style,omitempty"`
	HeaderName  string               `json:"header_name,omitempty"`
	TokenGrant  *openShellTokenGrant `json:"token_grant,omitempty"`
}

type openShellTokenGrant struct {
	GrantType            string                      `json:"grant_type,omitempty"`
	TokenEndpoint        string                      `json:"token_endpoint"`
	ClientAssertionType  string                      `json:"client_assertion_type,omitempty"`
	JWTSVIDAudience      string                      `json:"jwt_svid_audience,omitempty"`
	SubjectToken         *openShellSubjectToken      `json:"subject_token,omitempty"`
	AudienceOverrideList []openShellAudienceOverride `json:"audience_overrides,omitempty"`
}

type openShellSubjectToken struct {
	Source     string `json:"source"`
	Credential string `json:"credential"`
}

type openShellAudienceOverride struct {
	Host     string   `json:"host,omitempty"`
	Port     int32    `json:"port,omitempty"`
	Audience string   `json:"audience"`
	Scopes   []string `json:"scopes,omitempty"`
}

type openShellEndpoint struct {
	Host       string   `json:"host"`
	Port       int32    `json:"port,omitempty"`
	Protocol   string   `json:"protocol,omitempty"`
	TLS        string   `json:"tls,omitempty"`
	Access     string   `json:"access,omitempty"`
	AllowedIPs []string `json:"allowed_ips,omitempty"`
}

type openShellBinary struct {
	Path string `json:"path"`
}
