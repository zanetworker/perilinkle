package keycloak

import "context"

type Reconciler interface {
	EnsureRealm(ctx context.Context, realm Realm) error
	EnsureTargetService(ctx context.Context, realm Realm, target TargetService) error
	EnsureSandbox(ctx context.Context, realm Realm, sandbox Sandbox) error
	DeleteSandbox(ctx context.Context, realm Realm, sandbox Sandbox) error
	EnsureUsers(ctx context.Context, realm Realm, users []User) error
}

type Config struct {
	BaseURL       string
	Realm         string
	AdminRealm    string
	AdminUsername string
	AdminPassword string
	// AdminClientID and AdminClientSecret select a client_credentials service account
	// (realm-management roles only) instead of the admin user password grant.
	AdminClientID        string
	AdminClientSecret    string
	SPIFFETrustDomain    string
	SPIFFEBundleEndpoint string
	GatewayClientID      string
	GatewayAPIClientID   string
	GatewaySPIFFESubject string
	CLIClientID          string
}

type Realm struct {
	Name string
}

type TargetService struct {
	ClientID           string
	Namespace          string
	ServiceName        string
	ServiceAccountName string
	SPIFFESubject      string
}

type Sandbox struct {
	ID            string
	Namespace     string
	Name          string
	SPIFFESubject string
}

type User struct {
	Username      string
	FirstName     string
	LastName      string
	Email         string
	EmailVerified bool
	Enabled       bool
	Roles         []string
	Password      string
}
