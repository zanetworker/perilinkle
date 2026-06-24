package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ServiceGroup declares a group of target services available to sandboxes in a
// namespace.
type ServiceGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceGroupSpec   `json:"spec,omitempty"`
	Status ServiceGroupStatus `json:"status,omitempty"`
}

type ServiceGroupSpec struct {
	// ProviderProfileName is the OpenShell provider profile name to generate.
	// Defaults to metadata.name.
	ProviderProfileName string `json:"providerProfileName,omitempty"`

	// ProviderProfile configures optional OpenShell Gateway provider profile
	// material generated from this ServiceGroup.
	ProviderProfile ProviderProfileSpec `json:"providerProfile,omitempty"`

	// TargetServices is the set of services for which sandboxes may request
	// tokens.
	TargetServices []TargetService `json:"targetServices,omitempty"`
}

type ProviderProfileSpec struct {
	// Enabled controls whether OpenShell-native provider profile material is
	// generated for this ServiceGroup.
	Enabled bool `json:"enabled,omitempty"`

	// ID is the OpenShell provider profile ID. Defaults to providerProfileName,
	// then metadata.name.
	ID string `json:"id,omitempty"`

	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`

	// TokenEndpoint overrides the Keycloak token endpoint used by the generated
	// token grant.
	TokenEndpoint string `json:"tokenEndpoint,omitempty"`

	// JWTSVIDAudience overrides the audience requested for the sandbox JWT-SVID.
	JWTSVIDAudience string `json:"jwtSVIDAudience,omitempty"`

	// SubjectCredentialName is the provider credential containing the user token
	// used as the token-exchange subject token. Defaults to subject_token.
	SubjectCredentialName string `json:"subjectCredentialName,omitempty"`

	// AccessCredentialName is the bearer credential generated with token
	// exchange. Defaults to access_token.
	AccessCredentialName string `json:"accessCredentialName,omitempty"`

	// ClientAssertionType is the OAuth2 client_assertion_type value. Defaults to
	// urn:ietf:params:oauth:client-assertion-type:jwt-spiffe.
	ClientAssertionType string `json:"clientAssertionType,omitempty"`

	EndpointDefaults ProviderProfileEndpointDefaults `json:"endpointDefaults,omitempty"`
	Binaries         []ProviderProfileBinary         `json:"binaries,omitempty"`
}

type ProviderProfileEndpointDefaults struct {
	Port       int32    `json:"port,omitempty"`
	Protocol   string   `json:"protocol,omitempty"`
	TLS        string   `json:"tls,omitempty"`
	Access     string   `json:"access,omitempty"`
	AllowedIPs []string `json:"allowedIPs,omitempty"`
}

type ProviderProfileBinary struct {
	Path string `json:"path"`
}

type TargetService struct {
	// Name is the logical target name. It defaults the Keycloak client ID,
	// audience, service account name, and generated provider profile entry name.
	Name string `json:"name"`

	// ClientID is the Keycloak client ID and audience. Defaults to name.
	ClientID string `json:"clientID,omitempty"`

	// Description is carried into generated OpenShell provider profile material.
	Description string `json:"description,omitempty"`

	// Namespace is the Kubernetes namespace containing the target service.
	// Defaults to the service group namespace.
	Namespace string `json:"namespace,omitempty"`

	// ServiceAccountName is used when building the federated SPIFFE subject.
	// Defaults to metadata.name.
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// SPIFFEID overrides the generated SPIFFE ID. By default the ID is generated
	// as spiffe://<trust-domain>/ns/<namespace>/sa/<serviceAccountName>.
	SPIFFEID string `json:"spiffeID,omitempty"`

	// Endpoint overrides the OpenShell provider profile endpoint generated for
	// this target service.
	Endpoint TargetServiceEndpoint `json:"endpoint,omitempty"`

	// Token overrides the token grant audience metadata generated for this
	// target service.
	Token TargetServiceToken `json:"token,omitempty"`
}

type TargetServiceEndpoint struct {
	Host       string   `json:"host,omitempty"`
	Port       int32    `json:"port,omitempty"`
	Protocol   string   `json:"protocol,omitempty"`
	TLS        string   `json:"tls,omitempty"`
	Access     string   `json:"access,omitempty"`
	AllowedIPs []string `json:"allowedIPs,omitempty"`
}

type TargetServiceToken struct {
	Audience string   `json:"audience,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

type ServiceGroupStatus struct {
	ObservedGeneration             int64              `json:"observedGeneration,omitempty"`
	Ready                          bool               `json:"ready,omitempty"`
	ProviderProfileID              string             `json:"providerProfileID,omitempty"`
	ProviderProfileResourceVersion string             `json:"providerProfileResourceVersion,omitempty"`
	Conditions                     []metav1.Condition `json:"conditions,omitempty"`
}

// ServiceGroupList contains a list of ServiceGroup.
type ServiceGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceGroup `json:"items"`
}

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(
		SchemeGroupVersion,
		&ServiceGroup{},
		&ServiceGroupList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
