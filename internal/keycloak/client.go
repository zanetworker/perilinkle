package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	spiffeIdentityProviderAlias = "spiffe"
	openShellAdminRole          = "openshell-admin"
	openShellUserRole           = "openshell-user"
	openShellAllScope           = "openshell:all"
	targetScopeKindAttribute    = "perilinkle.dev/scope-kind"
	targetScopeKindValue        = "target-service"
)

var errNotFound = errors.New("keycloak object not found")

type Client struct {
	config Config
	http   *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time

	gatewayAccessToken string
	gatewayTokenExpiry time.Time
}

func NewClient(config Config) *Client {
	return &Client{
		config: config,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) EnsureRealm(ctx context.Context, realm Realm) error {
	existing := realmRepresentation{}
	err := c.doAdmin(ctx, http.MethodGet, "/admin/realms/"+url.PathEscape(realm.Name), nil, &existing)
	if errors.Is(err, errNotFound) {
		if err := c.doAdmin(ctx, http.MethodPost, "/admin/realms", realmRepresentation{
			Realm:       realm.Name,
			Enabled:     true,
			VerifyEmail: false,
		}, nil); err != nil {
			return fmt.Errorf("create realm %q: %w", realm.Name, err)
		}
	} else if err != nil {
		return fmt.Errorf("get realm %q: %w", realm.Name, err)
	}

	if err := c.ensureIdentityProvider(ctx, realm); err != nil {
		return err
	}
	if err := c.ensureRealmRole(ctx, realm, openShellAdminRole, "OpenShell Administrator"); err != nil {
		return err
	}
	if err := c.ensureRealmRole(ctx, realm, openShellUserRole, "OpenShell User"); err != nil {
		return err
	}

	cliScope, err := c.ensureAudienceScope(ctx, realm, c.config.CLIClientID, c.config.CLIClientID, map[string]string{})
	if err != nil {
		return err
	}
	gatewayScope, err := c.ensureAudienceScope(ctx, realm, c.config.GatewayClientID, c.config.GatewayClientID, map[string]string{})
	if err != nil {
		return err
	}
	cli, err := c.ensureCLIClient(ctx, realm)
	if err != nil {
		return err
	}
	if err := c.ensureDefaultClientScope(ctx, realm, cli.ID, cliScope.ID); err != nil {
		return err
	}
	if err := c.ensureDefaultClientScope(ctx, realm, cli.ID, gatewayScope.ID); err != nil {
		return err
	}
	if _, err := c.ensureFederatedClient(ctx, realm, c.config.GatewayClientID, c.config.GatewaySPIFFESubject); err != nil {
		return err
	}
	if c.config.GatewayAPIClientID != "" {
		if err := c.ensureGatewayAPIClient(ctx, realm); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) EnsureTargetService(ctx context.Context, realm Realm, target TargetService) error {
	if err := c.EnsureRealm(ctx, realm); err != nil {
		return err
	}
	if _, err := c.ensureAudienceScope(ctx, realm, target.ClientID, target.ClientID, map[string]string{
		targetScopeKindAttribute: targetScopeKindValue,
	}); err != nil {
		return err
	}
	client, err := c.ensureFederatedClient(ctx, realm, target.ClientID, target.SPIFFESubject)
	if err != nil {
		return err
	}
	scopes, err := c.targetClientScopes(ctx, realm)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		if err := c.ensureOptionalClientScope(ctx, realm, client.ID, scope.ID); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) EnsureSandbox(ctx context.Context, realm Realm, sandbox Sandbox) error {
	if err := c.EnsureRealm(ctx, realm); err != nil {
		return err
	}
	client, err := c.ensureFederatedClient(ctx, realm, sandbox.SPIFFESubject, sandbox.SPIFFESubject)
	if err != nil {
		return err
	}
	targetScopes, err := c.targetClientScopes(ctx, realm)
	if err != nil {
		return err
	}
	for _, scope := range targetScopes {
		if err := c.ensureDefaultClientScope(ctx, realm, client.ID, scope.ID); err != nil {
			return err
		}
	}
	sandboxScope, err := c.ensureAudienceScope(ctx, realm, sandbox.SPIFFESubject, sandbox.SPIFFESubject, map[string]string{})
	if err != nil {
		return err
	}
	gateway, err := c.clientByClientID(ctx, realm, c.config.GatewayClientID)
	if err != nil {
		return fmt.Errorf("get gateway client: %w", err)
	}
	if err := c.ensureDefaultClientScope(ctx, realm, gateway.ID, sandboxScope.ID); err != nil {
		return err
	}
	return nil
}

func (c *Client) DeleteSandbox(ctx context.Context, realm Realm, sandbox Sandbox) error {
	gateway, err := c.clientByClientID(ctx, realm, c.config.GatewayClientID)
	if err != nil && !errors.Is(err, errNotFound) {
		return fmt.Errorf("get gateway client: %w", err)
	}
	scope, err := c.clientScopeByName(ctx, realm, sandbox.SPIFFESubject)
	if err != nil && !errors.Is(err, errNotFound) {
		return fmt.Errorf("get sandbox audience scope: %w", err)
	}
	if gateway.ID != "" && scope.ID != "" {
		if err := c.doAdmin(ctx, http.MethodDelete, realmPath(realm, "/clients/"+gateway.ID+"/default-client-scopes/"+scope.ID), nil, nil); err != nil && !errors.Is(err, errNotFound) {
			return fmt.Errorf("remove gateway sandbox audience scope: %w", err)
		}
	}
	if scope.ID != "" {
		if err := c.doAdmin(ctx, http.MethodDelete, realmPath(realm, "/client-scopes/"+scope.ID), nil, nil); err != nil && !errors.Is(err, errNotFound) {
			return fmt.Errorf("delete sandbox audience scope: %w", err)
		}
	}
	client, err := c.clientByClientID(ctx, realm, sandbox.SPIFFESubject)
	if err != nil && !errors.Is(err, errNotFound) {
		return fmt.Errorf("get sandbox client: %w", err)
	}
	if client.ID != "" {
		if err := c.doAdmin(ctx, http.MethodDelete, realmPath(realm, "/clients/"+client.ID), nil, nil); err != nil && !errors.Is(err, errNotFound) {
			return fmt.Errorf("delete sandbox client: %w", err)
		}
	}
	return nil
}

func (c *Client) EnsureUsers(ctx context.Context, realm Realm, users []User) error {
	if err := c.EnsureRealm(ctx, realm); err != nil {
		return err
	}
	for _, user := range users {
		if strings.TrimSpace(user.Username) == "" {
			continue
		}
		if err := c.ensureUser(ctx, realm, user); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) ensureIdentityProvider(ctx context.Context, realm Realm) error {
	desired := identityProviderRepresentation{
		Alias:      spiffeIdentityProviderAlias,
		ProviderID: "spiffe",
		Enabled:    true,
		Config: map[string]string{
			"trustDomain":    c.config.SPIFFETrustDomain,
			"bundleEndpoint": c.config.SPIFFEBundleEndpoint,
		},
	}
	err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/identity-provider/instances/"+spiffeIdentityProviderAlias), nil, &identityProviderRepresentation{})
	if errors.Is(err, errNotFound) {
		if err := c.doAdmin(ctx, http.MethodPost, realmPath(realm, "/identity-provider/instances"), desired, nil); err != nil {
			return fmt.Errorf("create spiffe identity provider: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get spiffe identity provider: %w", err)
	}
	if err := c.doAdmin(ctx, http.MethodPut, realmPath(realm, "/identity-provider/instances/"+spiffeIdentityProviderAlias), desired, nil); err != nil {
		return fmt.Errorf("update spiffe identity provider: %w", err)
	}
	return nil
}

func (c *Client) ensureRealmRole(ctx context.Context, realm Realm, name, description string) error {
	err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/roles/"+url.PathEscape(name)), nil, &roleRepresentation{})
	if errors.Is(err, errNotFound) {
		return c.doAdmin(ctx, http.MethodPost, realmPath(realm, "/roles"), roleRepresentation{Name: name, Description: description}, nil)
	}
	return err
}

func (c *Client) ensureCLIClient(ctx context.Context, realm Realm) (clientRepresentation, error) {
	desired := clientRepresentation{
		ClientID:                  c.config.CLIClientID,
		Enabled:                   true,
		PublicClient:              true,
		DirectAccessGrantsEnabled: true,
		StandardFlowEnabled:       true,
		ImplicitFlowEnabled:       false,
		RedirectURIs:              []string{"http://127.0.0.1:*", "http://localhost:*"},
		Protocol:                  "openid-connect",
	}
	return c.ensureClient(ctx, realm, desired)
}

func (c *Client) ensureGatewayAPIClient(ctx context.Context, realm Realm) error {
	client, err := c.ensureConfidentialClient(ctx, realm, c.config.GatewayAPIClientID)
	if err != nil {
		return err
	}
	apiScope, err := c.ensureAudienceScope(ctx, realm, c.config.GatewayAPIClientID, c.config.GatewayAPIClientID, map[string]string{})
	if err != nil {
		return err
	}
	cliScope, err := c.ensureAudienceScope(ctx, realm, c.config.CLIClientID, c.config.CLIClientID, map[string]string{})
	if err != nil {
		return err
	}
	gatewayScope, err := c.ensureAudienceScope(ctx, realm, c.config.GatewayClientID, c.config.GatewayClientID, map[string]string{})
	if err != nil {
		return err
	}
	scopeScope, err := c.ensureScopeMapperScope(ctx, realm, c.config.GatewayAPIClientID+"-gateway-api-scope", openShellAllScope)
	if err != nil {
		return err
	}
	for _, scope := range []clientScopeRepresentation{apiScope, cliScope, gatewayScope, scopeScope} {
		if err := c.ensureDefaultClientScope(ctx, realm, client.ID, scope.ID); err != nil {
			return err
		}
	}
	return c.ensureServiceAccountRealmRole(ctx, realm, client.ID, openShellAdminRole)
}

func (c *Client) ensureConfidentialClient(ctx context.Context, realm Realm, clientID string) (clientRepresentation, error) {
	desired := clientRepresentation{
		ClientID:                  clientID,
		Enabled:                   true,
		PublicClient:              false,
		ServiceAccountsEnabled:    true,
		StandardFlowEnabled:       false,
		DirectAccessGrantsEnabled: false,
		ImplicitFlowEnabled:       false,
		ClientAuthenticatorType:   "client-secret",
		Protocol:                  "openid-connect",
		Attributes: map[string]string{
			"oauth2.device.authorization.grant.enabled": "false",
		},
	}
	return c.ensureClient(ctx, realm, desired)
}

func (c *Client) ensureFederatedClient(ctx context.Context, realm Realm, clientID, spiffeSubject string) (clientRepresentation, error) {
	desired := clientRepresentation{
		ClientID:                clientID,
		Enabled:                 true,
		ServiceAccountsEnabled:  true,
		StandardFlowEnabled:     true,
		ClientAuthenticatorType: "federated-jwt",
		Protocol:                "openid-connect",
		Attributes: map[string]string{
			"jwt.credential.issuer":                     spiffeIdentityProviderAlias,
			"jwt.credential.sub":                        spiffeSubject,
			"standard.token.exchange.enabled":           "true",
			"oauth2.device.authorization.grant.enabled": "false",
		},
	}
	return c.ensureClient(ctx, realm, desired)
}

func (c *Client) ensureClient(ctx context.Context, realm Realm, desired clientRepresentation) (clientRepresentation, error) {
	existing, err := c.clientByClientID(ctx, realm, desired.ClientID)
	if errors.Is(err, errNotFound) {
		if err := c.doAdmin(ctx, http.MethodPost, realmPath(realm, "/clients"), desired, nil); err != nil {
			return clientRepresentation{}, fmt.Errorf("create client %q: %w", desired.ClientID, err)
		}
		return c.clientByClientID(ctx, realm, desired.ClientID)
	}
	if err != nil {
		return clientRepresentation{}, err
	}
	desired.ID = existing.ID
	if desired.Attributes == nil {
		desired.Attributes = map[string]string{}
	}
	if err := c.doAdmin(ctx, http.MethodPut, realmPath(realm, "/clients/"+existing.ID), desired, nil); err != nil {
		return clientRepresentation{}, fmt.Errorf("update client %q: %w", desired.ClientID, err)
	}
	return desired, nil
}

func (c *Client) ensureAudienceScope(ctx context.Context, realm Realm, name, audience string, attributes map[string]string) (clientScopeRepresentation, error) {
	scope, err := c.clientScopeByName(ctx, realm, name)
	if errors.Is(err, errNotFound) {
		desired := clientScopeRepresentation{Name: name, Protocol: "openid-connect", Attributes: attributes}
		if err := c.doAdmin(ctx, http.MethodPost, realmPath(realm, "/client-scopes"), desired, nil); err != nil {
			return clientScopeRepresentation{}, fmt.Errorf("create client scope %q: %w", name, err)
		}
		scope, err = c.clientScopeByName(ctx, realm, name)
	}
	if err != nil {
		return clientScopeRepresentation{}, err
	}
	if attributes != nil {
		scope.Attributes = mergeStringMap(scope.Attributes, attributes)
		if err := c.doAdmin(ctx, http.MethodPut, realmPath(realm, "/client-scopes/"+scope.ID), scope, nil); err != nil {
			return clientScopeRepresentation{}, fmt.Errorf("update client scope %q: %w", name, err)
		}
	}
	if err := c.ensureAudienceMapper(ctx, realm, scope.ID, name+"-audience-mapper", audience); err != nil {
		return clientScopeRepresentation{}, err
	}
	return scope, nil
}

func (c *Client) ensureAudienceMapper(ctx context.Context, realm Realm, scopeID, name, audience string) error {
	var mappers []protocolMapperRepresentation
	if err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/client-scopes/"+scopeID+"/protocol-mappers/models"), nil, &mappers); err != nil {
		return fmt.Errorf("list protocol mappers: %w", err)
	}
	desired := protocolMapperRepresentation{
		Name:           name,
		Protocol:       "openid-connect",
		ProtocolMapper: "oidc-audience-mapper",
		Config: map[string]string{
			"included.client.audience": audience,
			"access.token.claim":       "true",
		},
	}
	for _, mapper := range mappers {
		if mapper.Name == name {
			desired.ID = mapper.ID
			return c.doAdmin(ctx, http.MethodPut, realmPath(realm, "/client-scopes/"+scopeID+"/protocol-mappers/models/"+mapper.ID), desired, nil)
		}
	}
	return c.doAdmin(ctx, http.MethodPost, realmPath(realm, "/client-scopes/"+scopeID+"/protocol-mappers/models"), desired, nil)
}

func (c *Client) ensureScopeMapperScope(ctx context.Context, realm Realm, name, scope string) (clientScopeRepresentation, error) {
	clientScope, err := c.clientScopeByName(ctx, realm, name)
	if errors.Is(err, errNotFound) {
		desired := clientScopeRepresentation{Name: name, Protocol: "openid-connect"}
		if err := c.doAdmin(ctx, http.MethodPost, realmPath(realm, "/client-scopes"), desired, nil); err != nil {
			return clientScopeRepresentation{}, fmt.Errorf("create client scope %q: %w", name, err)
		}
		clientScope, err = c.clientScopeByName(ctx, realm, name)
	}
	if err != nil {
		return clientScopeRepresentation{}, err
	}
	if err := c.ensureHardcodedClaimMapper(ctx, realm, clientScope.ID, name+"-scope-mapper", "scope", scope); err != nil {
		return clientScopeRepresentation{}, err
	}
	return clientScope, nil
}

func (c *Client) ensureHardcodedClaimMapper(ctx context.Context, realm Realm, scopeID, name, claimName, claimValue string) error {
	var mappers []protocolMapperRepresentation
	if err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/client-scopes/"+scopeID+"/protocol-mappers/models"), nil, &mappers); err != nil {
		return fmt.Errorf("list protocol mappers: %w", err)
	}
	desired := protocolMapperRepresentation{
		Name:           name,
		Protocol:       "openid-connect",
		ProtocolMapper: "oidc-hardcoded-claim-mapper",
		Config: map[string]string{
			"claim.name":           claimName,
			"claim.value":          claimValue,
			"jsonType.label":       "String",
			"access.token.claim":   "true",
			"id.token.claim":       "false",
			"userinfo.token.claim": "false",
		},
	}
	for _, mapper := range mappers {
		if mapper.Name == name {
			desired.ID = mapper.ID
			return c.doAdmin(ctx, http.MethodPut, realmPath(realm, "/client-scopes/"+scopeID+"/protocol-mappers/models/"+mapper.ID), desired, nil)
		}
	}
	return c.doAdmin(ctx, http.MethodPost, realmPath(realm, "/client-scopes/"+scopeID+"/protocol-mappers/models"), desired, nil)
}

func (c *Client) ensureDefaultClientScope(ctx context.Context, realm Realm, clientID, scopeID string) error {
	err := c.doAdmin(ctx, http.MethodPut, realmPath(realm, "/clients/"+clientID+"/default-client-scopes/"+scopeID), nil, nil)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

func (c *Client) ensureOptionalClientScope(ctx context.Context, realm Realm, clientID, scopeID string) error {
	err := c.doAdmin(ctx, http.MethodPut, realmPath(realm, "/clients/"+clientID+"/optional-client-scopes/"+scopeID), nil, nil)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

func (c *Client) targetClientScopes(ctx context.Context, realm Realm) ([]clientScopeRepresentation, error) {
	var scopes []clientScopeRepresentation
	if err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/client-scopes"), nil, &scopes); err != nil {
		return nil, fmt.Errorf("list client scopes: %w", err)
	}
	var targets []clientScopeRepresentation
	for _, scope := range scopes {
		if scope.Attributes[targetScopeKindAttribute] == targetScopeKindValue {
			targets = append(targets, scope)
		}
	}
	return targets, nil
}

func (c *Client) ensureUser(ctx context.Context, realm Realm, desired User) error {
	user, err := c.userByUsername(ctx, realm, desired.Username)
	if errors.Is(err, errNotFound) {
		rep := userRepresentation{
			Username:        desired.Username,
			FirstName:       desired.FirstName,
			LastName:        desired.LastName,
			Email:           desired.Email,
			Enabled:         desired.Enabled,
			EmailVerified:   desired.EmailVerified,
			RequiredActions: []string{},
		}
		if err := c.doAdmin(ctx, http.MethodPost, realmPath(realm, "/users"), rep, nil); err != nil {
			return fmt.Errorf("create user %q: %w", desired.Username, err)
		}
		user, err = c.userByUsername(ctx, realm, desired.Username)
		if err != nil {
			return err
		}
		if desired.Password != "" {
			if err := c.resetUserPassword(ctx, realm, user.ID, desired.Password); err != nil {
				return err
			}
		}
	} else if err != nil {
		return err
	}
	for _, roleName := range desired.Roles {
		role, err := c.realmRole(ctx, realm, roleName)
		if err != nil {
			return err
		}
		if err := c.doAdmin(ctx, http.MethodPost, realmPath(realm, "/users/"+user.ID+"/role-mappings/realm"), []roleRepresentation{role}, nil); err != nil {
			return fmt.Errorf("assign role %q to user %q: %w", roleName, desired.Username, err)
		}
	}
	return nil
}

func (c *Client) ensureServiceAccountRealmRole(ctx context.Context, realm Realm, clientUUID, roleName string) error {
	var user userRepresentation
	if err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/clients/"+clientUUID+"/service-account-user"), nil, &user); err != nil {
		return fmt.Errorf("get service account user for client %q: %w", clientUUID, err)
	}
	role, err := c.realmRole(ctx, realm, roleName)
	if err != nil {
		return err
	}
	if err := c.doAdmin(ctx, http.MethodPost, realmPath(realm, "/users/"+user.ID+"/role-mappings/realm"), []roleRepresentation{role}, nil); err != nil {
		return fmt.Errorf("assign role %q to service account for client %q: %w", roleName, clientUUID, err)
	}
	return nil
}

func (c *Client) resetUserPassword(ctx context.Context, realm Realm, userID, password string) error {
	return c.doAdmin(ctx, http.MethodPut, realmPath(realm, "/users/"+userID+"/reset-password"), map[string]any{
		"type":      "password",
		"value":     password,
		"temporary": false,
	}, nil)
}

func (c *Client) realmRole(ctx context.Context, realm Realm, name string) (roleRepresentation, error) {
	var role roleRepresentation
	if err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/roles/"+url.PathEscape(name)), nil, &role); err != nil {
		return roleRepresentation{}, fmt.Errorf("get realm role %q: %w", name, err)
	}
	return role, nil
}

func (c *Client) clientByClientID(ctx context.Context, realm Realm, clientID string) (clientRepresentation, error) {
	var clients []clientRepresentation
	if err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/clients")+"?clientId="+url.QueryEscape(clientID), nil, &clients); err != nil {
		return clientRepresentation{}, err
	}
	for _, client := range clients {
		if client.ClientID == clientID {
			return client, nil
		}
	}
	return clientRepresentation{}, errNotFound
}

func (c *Client) clientScopeByName(ctx context.Context, realm Realm, name string) (clientScopeRepresentation, error) {
	var scopes []clientScopeRepresentation
	if err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/client-scopes"), nil, &scopes); err != nil {
		return clientScopeRepresentation{}, err
	}
	for _, scope := range scopes {
		if scope.Name == name {
			return scope, nil
		}
	}
	return clientScopeRepresentation{}, errNotFound
}

func (c *Client) userByUsername(ctx context.Context, realm Realm, username string) (userRepresentation, error) {
	var users []userRepresentation
	if err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/users")+"?username="+url.QueryEscape(username)+"&exact=true", nil, &users); err != nil {
		return userRepresentation{}, err
	}
	for _, user := range users {
		if user.Username == username {
			return user, nil
		}
	}
	return userRepresentation{}, errNotFound
}

func (c *Client) doAdmin(ctx context.Context, method, requestPath string, in, out any) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	err := c.do(ctx, method, requestPath, in, out, true)
	if isUnauthorized(err) {
		c.clearToken()
		if tokenErr := c.ensureToken(ctx); tokenErr != nil {
			return tokenErr
		}
		err = c.do(ctx, method, requestPath, in, out, true)
	}
	return err
}

func (c *Client) do(ctx context.Context, method, requestPath string, in, out any, authenticated bool) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.config.BaseURL, "/")+requestPath, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		c.mu.Lock()
		token := c.accessToken
		c.mu.Unlock()
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return unauthorizedError{}
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("keycloak %s %s failed: status=%d body=%s", method, requestPath, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry.Add(-30*time.Second)) {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	values := url.Values{}
	if c.config.AdminClientID != "" {
		values.Set("grant_type", "client_credentials")
		values.Set("client_id", c.config.AdminClientID)
		values.Set("client_secret", c.config.AdminClientSecret)
	} else {
		values.Set("grant_type", "password")
		values.Set("client_id", "admin-cli")
		values.Set("username", c.config.AdminUsername)
		values.Set("password", c.config.AdminPassword)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.BaseURL, "/")+"/realms/"+url.PathEscape(c.config.AdminRealm)+"/protocol/openid-connect/token", strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("keycloak admin token request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return err
	}
	c.mu.Lock()
	c.accessToken = token.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	c.mu.Unlock()
	return nil
}

func (c *Client) clearToken() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accessToken = ""
	c.tokenExpiry = time.Time{}
}

func (c *Client) GatewayAccessToken(ctx context.Context, realm Realm) (string, error) {
	if c.config.GatewayAPIClientID == "" {
		return "", fmt.Errorf("gateway API client ID is not configured")
	}
	c.mu.Lock()
	if c.gatewayAccessToken != "" && time.Now().Before(c.gatewayTokenExpiry.Add(-30*time.Second)) {
		token := c.gatewayAccessToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	if err := c.EnsureRealm(ctx, realm); err != nil {
		return "", err
	}
	secret, err := c.gatewayAPIClientSecret(ctx, realm)
	if err != nil {
		return "", err
	}
	values := url.Values{}
	values.Set("grant_type", "client_credentials")
	values.Set("client_id", c.config.GatewayAPIClientID)
	values.Set("client_secret", secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.BaseURL, "/")+"/realms/"+url.PathEscape(realm.Name)+"/protocol/openid-connect/token", strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("keycloak gateway token request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.gatewayAccessToken = token.AccessToken
	c.gatewayTokenExpiry = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	c.mu.Unlock()
	return token.AccessToken, nil
}

func (c *Client) gatewayAPIClientSecret(ctx context.Context, realm Realm) (string, error) {
	client, err := c.clientByClientID(ctx, realm, c.config.GatewayAPIClientID)
	if err != nil {
		return "", fmt.Errorf("get gateway API client: %w", err)
	}
	var secret clientSecretRepresentation
	if err := c.doAdmin(ctx, http.MethodGet, realmPath(realm, "/clients/"+client.ID+"/client-secret"), nil, &secret); err != nil {
		return "", fmt.Errorf("get gateway API client secret: %w", err)
	}
	if secret.Value == "" {
		return "", fmt.Errorf("gateway API client secret is empty")
	}
	return secret.Value, nil
}

func realmPath(realm Realm, suffix string) string {
	return path.Clean("/admin/realms/" + realm.Name + "/" + strings.TrimPrefix(suffix, "/"))
}

func mergeStringMap(base, overlay map[string]string) map[string]string {
	if base == nil {
		base = map[string]string{}
	}
	for key, value := range overlay {
		base[key] = value
	}
	return base
}

type unauthorizedError struct{}

func (unauthorizedError) Error() string { return "keycloak unauthorized" }

func isUnauthorized(err error) bool {
	var unauthorized unauthorizedError
	return errors.As(err, &unauthorized)
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type realmRepresentation struct {
	Realm       string `json:"realm,omitempty"`
	Enabled     bool   `json:"enabled"`
	VerifyEmail bool   `json:"verifyEmail"`
}

type identityProviderRepresentation struct {
	Alias      string            `json:"alias,omitempty"`
	ProviderID string            `json:"providerId,omitempty"`
	Enabled    bool              `json:"enabled"`
	Config     map[string]string `json:"config,omitempty"`
}

type roleRepresentation struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

type clientRepresentation struct {
	ID                        string            `json:"id,omitempty"`
	ClientID                  string            `json:"clientId,omitempty"`
	Enabled                   bool              `json:"enabled"`
	PublicClient              bool              `json:"publicClient"`
	ServiceAccountsEnabled    bool              `json:"serviceAccountsEnabled"`
	StandardFlowEnabled       bool              `json:"standardFlowEnabled"`
	DirectAccessGrantsEnabled bool              `json:"directAccessGrantsEnabled"`
	ImplicitFlowEnabled       bool              `json:"implicitFlowEnabled"`
	ClientAuthenticatorType   string            `json:"clientAuthenticatorType,omitempty"`
	RedirectURIs              []string          `json:"redirectUris,omitempty"`
	Protocol                  string            `json:"protocol,omitempty"`
	Attributes                map[string]string `json:"attributes,omitempty"`
}

type clientSecretRepresentation struct {
	Type  string `json:"type,omitempty"`
	Value string `json:"value,omitempty"`
}

type clientScopeRepresentation struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Protocol   string            `json:"protocol,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type protocolMapperRepresentation struct {
	ID             string            `json:"id,omitempty"`
	Name           string            `json:"name,omitempty"`
	Protocol       string            `json:"protocol,omitempty"`
	ProtocolMapper string            `json:"protocolMapper,omitempty"`
	Config         map[string]string `json:"config,omitempty"`
}

type userRepresentation struct {
	ID              string   `json:"id,omitempty"`
	Username        string   `json:"username,omitempty"`
	FirstName       string   `json:"firstName,omitempty"`
	LastName        string   `json:"lastName,omitempty"`
	Email           string   `json:"email,omitempty"`
	Enabled         bool     `json:"enabled"`
	EmailVerified   bool     `json:"emailVerified"`
	RequiredActions []string `json:"requiredActions,omitempty"`
}
