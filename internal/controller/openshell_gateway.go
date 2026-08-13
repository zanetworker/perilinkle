package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/gsim/perilinkle/internal/keycloak"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type OpenShellGateway interface {
	UpsertProviderProfile(ctx context.Context, profile *openShellProviderProfile) (string, error)
	DeleteProviderProfile(ctx context.Context, id string) error
	SetGlobalBoolSetting(ctx context.Context, key string, value bool) (uint64, error)
}

type OpenShellGatewayConfig struct {
	Address     string
	Insecure    bool
	CAFile      string
	TokenSource OpenShellGatewayTokenSource
	Realm       keycloak.Realm
}

type openShellGatewayClient struct {
	conn        *grpc.ClientConn
	types       *openShellProtoTypes
	tokenSource OpenShellGatewayTokenSource
	realm       keycloak.Realm
}

type OpenShellGatewayTokenSource interface {
	GatewayAccessToken(ctx context.Context, realm keycloak.Realm) (string, error)
}

func NewOpenShellGatewayClient(config OpenShellGatewayConfig) (OpenShellGateway, error) {
	if config.Address == "" {
		return nil, nil
	}
	options := []grpc.DialOption{}
	if config.Insecure {
		options = append(options, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		tlsConfig, err := openShellGatewayTLSConfig(config.CAFile)
		if err != nil {
			return nil, err
		}
		options = append(options, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	}
	conn, err := grpc.Dial(config.Address, options...)
	if err != nil {
		return nil, fmt.Errorf("connect openshell gateway: %w", err)
	}
	types, err := newOpenShellProtoTypes()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &openShellGatewayClient{
		conn:        conn,
		types:       types,
		tokenSource: config.TokenSource,
		realm:       config.Realm,
	}, nil
}

func openShellGatewayTLSConfig(caFile string) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile == "" {
		return config, nil
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	data, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read openshell gateway CA file %q: %w", caFile, err)
	}
	if ok := roots.AppendCertsFromPEM(data); !ok {
		return nil, fmt.Errorf("parse openshell gateway CA file %q: no PEM certificates found", caFile)
	}
	config.RootCAs = roots
	return config, nil
}

func (c *openShellGatewayClient) UpsertProviderProfile(ctx context.Context, profile *openShellProviderProfile) (string, error) {
	if profile == nil {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	desired := c.providerProfileMessage(profile)
	existing, resourceVersion, err := c.getProviderProfile(ctx, profile.ID)
	if status.Code(err) == codes.NotFound {
		return c.importProviderProfile(ctx, desired)
	}
	if err != nil {
		return "", err
	}
	if dynamicProfileEqual(existing, desired, c.types.providerProfile.Fields().ByName("resource_version")) {
		return fmt.Sprintf("%d", resourceVersion), nil
	}
	return c.updateProviderProfile(ctx, profile.ID, desired, resourceVersion)
}

func (c *openShellGatewayClient) DeleteProviderProfile(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req := dynamicpb.NewMessage(c.types.deleteProviderProfileRequest)
	req.Set(c.types.deleteProviderProfileRequest.Fields().ByName("id"), protoreflect.ValueOfString(id))
	resp := dynamicpb.NewMessage(c.types.deleteProviderProfileResponse)
	err := c.invoke(ctx, "/openshell.v1.OpenShell/DeleteProviderProfile", req, resp)
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

func (c *openShellGatewayClient) SetGlobalBoolSetting(ctx context.Context, key string, value bool) (uint64, error) {
	if strings.TrimSpace(key) == "" {
		return 0, fmt.Errorf("setting key is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	setting := dynamicpb.NewMessage(c.types.settingValue)
	setting.Set(c.types.settingValue.Fields().ByName("bool_value"), protoreflect.ValueOfBool(value))

	req := dynamicpb.NewMessage(c.types.updateConfigRequest)
	req.Set(c.types.updateConfigRequest.Fields().ByName("setting_key"), protoreflect.ValueOfString(key))
	req.Set(c.types.updateConfigRequest.Fields().ByName("setting_value"), protoreflect.ValueOfMessage(setting))
	req.Set(c.types.updateConfigRequest.Fields().ByName("global"), protoreflect.ValueOfBool(true))

	resp := dynamicpb.NewMessage(c.types.updateConfigResponse)
	if err := c.invoke(ctx, "/openshell.v1.OpenShell/UpdateConfig", req, resp); err != nil {
		return 0, fmt.Errorf("set global setting %q: %w", key, err)
	}
	revision := resp.Get(c.types.updateConfigResponse.Fields().ByName("settings_revision")).Uint()
	return revision, nil
}

func (c *openShellGatewayClient) lintProviderProfile(ctx context.Context, profile *dynamicpb.Message) error {
	req := dynamicpb.NewMessage(c.types.lintProviderProfilesRequest)
	appendImportItem(req.Mutable(c.types.lintProviderProfilesRequest.Fields().ByName("profiles")).List(), c.importItemMessage(profile))
	resp := dynamicpb.NewMessage(c.types.lintProviderProfilesResponse)
	if err := c.invoke(ctx, "/openshell.v1.OpenShell/LintProviderProfiles", req, resp); err != nil {
		return fmt.Errorf("lint provider profile: %w", err)
	}
	if err := diagnosticsError("openshell provider profile validation failed", resp.Get(c.types.lintProviderProfilesResponse.Fields().ByName("diagnostics")).List()); err != nil {
		return err
	}
	return nil
}

func (c *openShellGatewayClient) getProviderProfile(ctx context.Context, id string) (*dynamicpb.Message, uint64, error) {
	req := dynamicpb.NewMessage(c.types.getProviderProfileRequest)
	req.Set(c.types.getProviderProfileRequest.Fields().ByName("id"), protoreflect.ValueOfString(id))
	resp := dynamicpb.NewMessage(c.types.providerProfileResponse)
	if err := c.invoke(ctx, "/openshell.v1.OpenShell/GetProviderProfile", req, resp); err != nil {
		return nil, 0, err
	}
	profile := resp.Mutable(c.types.providerProfileResponse.Fields().ByName("profile")).Message().Interface().(*dynamicpb.Message)
	resourceVersion := profile.Get(c.types.providerProfile.Fields().ByName("resource_version")).Uint()
	return profile, resourceVersion, nil
}

func (c *openShellGatewayClient) importProviderProfile(ctx context.Context, profile *dynamicpb.Message) (string, error) {
	req := dynamicpb.NewMessage(c.types.importProviderProfilesRequest)
	appendImportItem(req.Mutable(c.types.importProviderProfilesRequest.Fields().ByName("profiles")).List(), c.importItemMessage(profile))
	resp := dynamicpb.NewMessage(c.types.importProviderProfilesResponse)
	if err := c.invoke(ctx, "/openshell.v1.OpenShell/ImportProviderProfiles", req, resp); err != nil {
		return "", fmt.Errorf("import provider profile: %w", err)
	}
	if err := diagnosticsError("openshell provider profile import failed", resp.Get(c.types.importProviderProfilesResponse.Fields().ByName("diagnostics")).List()); err != nil {
		return "", err
	}
	profiles := resp.Get(c.types.importProviderProfilesResponse.Fields().ByName("profiles")).List()
	if profiles.Len() == 0 {
		return "", nil
	}
	imported := profiles.Get(0).Message()
	resourceVersion := imported.Get(c.types.providerProfile.Fields().ByName("resource_version")).Uint()
	return fmt.Sprintf("%d", resourceVersion), nil
}

func (c *openShellGatewayClient) updateProviderProfile(ctx context.Context, id string, profile *dynamicpb.Message, resourceVersion uint64) (string, error) {
	req := dynamicpb.NewMessage(c.types.updateProviderProfilesRequest)
	req.Set(c.types.updateProviderProfilesRequest.Fields().ByName("id"), protoreflect.ValueOfString(id))
	req.Set(c.types.updateProviderProfilesRequest.Fields().ByName("expected_resource_version"), protoreflect.ValueOfUint64(resourceVersion))
	req.Set(c.types.updateProviderProfilesRequest.Fields().ByName("profile"), protoreflect.ValueOfMessage(c.importItemMessage(profile)))
	resp := dynamicpb.NewMessage(c.types.updateProviderProfilesResponse)
	if err := c.invoke(ctx, "/openshell.v1.OpenShell/UpdateProviderProfiles", req, resp); err != nil {
		return "", fmt.Errorf("update provider profile: %w", err)
	}
	if err := diagnosticsError("openshell provider profile update failed", resp.Get(c.types.updateProviderProfilesResponse.Fields().ByName("diagnostics")).List()); err != nil {
		return "", err
	}
	updated := resp.Get(c.types.updateProviderProfilesResponse.Fields().ByName("profile")).Message()
	nextVersion := updated.Get(c.types.providerProfile.Fields().ByName("resource_version")).Uint()
	return fmt.Sprintf("%d", nextVersion), nil
}

func (c *openShellGatewayClient) invoke(ctx context.Context, method string, req, resp proto.Message) error {
	if c.tokenSource != nil {
		token, err := c.tokenSource.GatewayAccessToken(ctx, c.realm)
		if err != nil {
			return fmt.Errorf("get openshell gateway access token: %w", err)
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	}
	return c.conn.Invoke(ctx, method, req, resp)
}

func (c *openShellGatewayClient) importItemMessage(profile *dynamicpb.Message) protoreflect.Message {
	item := dynamicpb.NewMessage(c.types.providerProfileImportItem)
	item.Set(c.types.providerProfileImportItem.Fields().ByName("profile"), protoreflect.ValueOfMessage(profile))
	item.Set(c.types.providerProfileImportItem.Fields().ByName("source"), protoreflect.ValueOfString("perilinkle"))
	return item
}

func (c *openShellGatewayClient) providerProfileMessage(profile *openShellProviderProfile) *dynamicpb.Message {
	msg := dynamicpb.NewMessage(c.types.providerProfile)
	msg.Set(c.types.providerProfile.Fields().ByName("id"), protoreflect.ValueOfString(profile.ID))
	msg.Set(c.types.providerProfile.Fields().ByName("display_name"), protoreflect.ValueOfString(profile.DisplayName))
	msg.Set(c.types.providerProfile.Fields().ByName("description"), protoreflect.ValueOfString(profile.Description))
	msg.Set(c.types.providerProfile.Fields().ByName("category"), protoreflect.ValueOfEnum(providerProfileCategoryNumber(profile.Category)))

	credentials := msg.Mutable(c.types.providerProfile.Fields().ByName("credentials")).List()
	for _, credential := range profile.Credentials {
		credentials.Append(protoreflect.ValueOfMessage(c.providerCredentialMessage(credential)))
	}
	endpoints := msg.Mutable(c.types.providerProfile.Fields().ByName("endpoints")).List()
	for _, endpoint := range profile.Endpoints {
		endpoints.Append(protoreflect.ValueOfMessage(c.networkEndpointMessage(endpoint)))
	}
	binaries := msg.Mutable(c.types.providerProfile.Fields().ByName("binaries")).List()
	for _, binary := range profile.Binaries {
		binaries.Append(protoreflect.ValueOfMessage(c.networkBinaryMessage(binary)))
	}
	return msg
}

func (c *openShellGatewayClient) providerCredentialMessage(credential openShellProviderCredential) protoreflect.Message {
	msg := dynamicpb.NewMessage(c.types.providerProfileCredential)
	msg.Set(c.types.providerProfileCredential.Fields().ByName("name"), protoreflect.ValueOfString(credential.Name))
	msg.Set(c.types.providerProfileCredential.Fields().ByName("description"), protoreflect.ValueOfString(credential.Description))
	msg.Set(c.types.providerProfileCredential.Fields().ByName("auth_style"), protoreflect.ValueOfString(credential.AuthStyle))
	msg.Set(c.types.providerProfileCredential.Fields().ByName("header_name"), protoreflect.ValueOfString(credential.HeaderName))
	if credential.TokenGrant != nil {
		msg.Set(c.types.providerProfileCredential.Fields().ByName("token_grant"), protoreflect.ValueOfMessage(c.tokenGrantMessage(*credential.TokenGrant)))
	}
	return msg
}

func (c *openShellGatewayClient) tokenGrantMessage(grant openShellTokenGrant) protoreflect.Message {
	msg := dynamicpb.NewMessage(c.types.providerCredentialTokenGrant)
	msg.Set(c.types.providerCredentialTokenGrant.Fields().ByName("token_endpoint"), protoreflect.ValueOfString(grant.TokenEndpoint))
	msg.Set(c.types.providerCredentialTokenGrant.Fields().ByName("jwt_svid_audience"), protoreflect.ValueOfString(grant.JWTSVIDAudience))
	msg.Set(c.types.providerCredentialTokenGrant.Fields().ByName("client_assertion_type"), protoreflect.ValueOfString(grant.ClientAssertionType))
	msg.Set(c.types.providerCredentialTokenGrant.Fields().ByName("grant_type"), protoreflect.ValueOfEnum(2))
	if grant.SubjectToken != nil {
		subject := dynamicpb.NewMessage(c.types.providerCredentialTokenGrantSubjectToken)
		subject.Set(c.types.providerCredentialTokenGrantSubjectToken.Fields().ByName("source"), protoreflect.ValueOfString(grant.SubjectToken.Source))
		subject.Set(c.types.providerCredentialTokenGrantSubjectToken.Fields().ByName("credential"), protoreflect.ValueOfString(grant.SubjectToken.Credential))
		msg.Set(c.types.providerCredentialTokenGrant.Fields().ByName("subject_token"), protoreflect.ValueOfMessage(subject))
	}
	overrides := msg.Mutable(c.types.providerCredentialTokenGrant.Fields().ByName("audience_overrides")).List()
	for _, override := range grant.AudienceOverrideList {
		overrides.Append(protoreflect.ValueOfMessage(c.audienceOverrideMessage(override)))
	}
	return msg
}

func (c *openShellGatewayClient) audienceOverrideMessage(override openShellAudienceOverride) protoreflect.Message {
	msg := dynamicpb.NewMessage(c.types.providerCredentialTokenGrantAudienceOverride)
	msg.Set(c.types.providerCredentialTokenGrantAudienceOverride.Fields().ByName("host"), protoreflect.ValueOfString(override.Host))
	msg.Set(c.types.providerCredentialTokenGrantAudienceOverride.Fields().ByName("port"), protoreflect.ValueOfUint32(uint32(override.Port)))
	msg.Set(c.types.providerCredentialTokenGrantAudienceOverride.Fields().ByName("audience"), protoreflect.ValueOfString(override.Audience))
	scopes := msg.Mutable(c.types.providerCredentialTokenGrantAudienceOverride.Fields().ByName("scopes")).List()
	for _, scope := range override.Scopes {
		scopes.Append(protoreflect.ValueOfString(scope))
	}
	return msg
}

func (c *openShellGatewayClient) networkEndpointMessage(endpoint openShellEndpoint) protoreflect.Message {
	msg := dynamicpb.NewMessage(c.types.networkEndpoint)
	msg.Set(c.types.networkEndpoint.Fields().ByName("host"), protoreflect.ValueOfString(endpoint.Host))
	msg.Set(c.types.networkEndpoint.Fields().ByName("port"), protoreflect.ValueOfUint32(uint32(endpoint.Port)))
	msg.Set(c.types.networkEndpoint.Fields().ByName("protocol"), protoreflect.ValueOfString(endpoint.Protocol))
	msg.Set(c.types.networkEndpoint.Fields().ByName("tls"), protoreflect.ValueOfString(endpoint.TLS))
	msg.Set(c.types.networkEndpoint.Fields().ByName("access"), protoreflect.ValueOfString(endpoint.Access))
	allowedIPs := msg.Mutable(c.types.networkEndpoint.Fields().ByName("allowed_ips")).List()
	for _, allowedIP := range endpoint.AllowedIPs {
		allowedIPs.Append(protoreflect.ValueOfString(allowedIP))
	}
	return msg
}

func (c *openShellGatewayClient) networkBinaryMessage(binary openShellBinary) protoreflect.Message {
	msg := dynamicpb.NewMessage(c.types.networkBinary)
	msg.Set(c.types.networkBinary.Fields().ByName("path"), protoreflect.ValueOfString(binary.Path))
	return msg
}

func appendImportItem(list protoreflect.List, item protoreflect.Message) {
	list.Append(protoreflect.ValueOfMessage(item))
}

func diagnosticsError(prefix string, diagnostics protoreflect.List) error {
	var messages []string
	for i := 0; i < diagnostics.Len(); i++ {
		diagnostic := diagnostics.Get(i).Message()
		severity := diagnostic.Get(diagnostic.Descriptor().Fields().ByName("severity")).String()
		if severity == "error" {
			messages = append(messages, diagnosticMessage(diagnostic))
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %s", prefix, strings.Join(messages, "; "))
}

func diagnosticMessage(diagnostic protoreflect.Message) string {
	fieldValue := func(name protoreflect.Name) string {
		field := diagnostic.Descriptor().Fields().ByName(name)
		if field == nil {
			return ""
		}
		return diagnostic.Get(field).String()
	}
	field := fieldValue("field")
	message := fieldValue("message")
	profileID := fieldValue("profile_id")
	source := fieldValue("source")
	parts := []string{}
	if profileID != "" {
		parts = append(parts, "profile="+profileID)
	}
	if field != "" {
		parts = append(parts, "field="+field)
	}
	if source != "" {
		parts = append(parts, "source="+source)
	}
	if len(parts) == 0 {
		return message
	}
	if message == "" {
		return strings.Join(parts, " ")
	}
	return strings.Join(parts, " ") + ": " + message
}

func dynamicProfileEqual(left, right *dynamicpb.Message, resourceVersion protoreflect.FieldDescriptor) bool {
	leftCopy := proto.Clone(left).(*dynamicpb.Message)
	rightCopy := proto.Clone(right).(*dynamicpb.Message)
	leftCopy.Clear(resourceVersion)
	rightCopy.Clear(resourceVersion)
	options := protojson.MarshalOptions{}
	leftJSON, leftErr := options.Marshal(leftCopy)
	rightJSON, rightErr := options.Marshal(rightCopy)
	if leftErr != nil || rightErr != nil {
		return reflect.DeepEqual(leftCopy, rightCopy)
	}
	return string(leftJSON) == string(rightJSON)
}

type openShellProtoTypes struct {
	providerProfile                              protoreflect.MessageDescriptor
	providerProfileCredential                    protoreflect.MessageDescriptor
	providerCredentialTokenGrant                 protoreflect.MessageDescriptor
	providerCredentialTokenGrantSubjectToken     protoreflect.MessageDescriptor
	providerCredentialTokenGrantAudienceOverride protoreflect.MessageDescriptor
	providerProfileImportItem                    protoreflect.MessageDescriptor
	providerProfileDiagnostic                    protoreflect.MessageDescriptor
	networkEndpoint                              protoreflect.MessageDescriptor
	networkBinary                                protoreflect.MessageDescriptor
	getProviderProfileRequest                    protoreflect.MessageDescriptor
	providerProfileResponse                      protoreflect.MessageDescriptor
	importProviderProfilesRequest                protoreflect.MessageDescriptor
	importProviderProfilesResponse               protoreflect.MessageDescriptor
	updateProviderProfilesRequest                protoreflect.MessageDescriptor
	updateProviderProfilesResponse               protoreflect.MessageDescriptor
	lintProviderProfilesRequest                  protoreflect.MessageDescriptor
	lintProviderProfilesResponse                 protoreflect.MessageDescriptor
	deleteProviderProfileRequest                 protoreflect.MessageDescriptor
	deleteProviderProfileResponse                protoreflect.MessageDescriptor
	settingValue                                 protoreflect.MessageDescriptor
	updateConfigRequest                          protoreflect.MessageDescriptor
	updateConfigResponse                         protoreflect.MessageDescriptor
}

func newOpenShellProtoTypes() (*openShellProtoTypes, error) {
	files, err := protodesc.NewFiles(openShellDescriptor())
	if err != nil {
		return nil, fmt.Errorf("build openshell proto descriptors: %w", err)
	}
	message := func(name protoreflect.FullName) protoreflect.MessageDescriptor {
		desc, _ := files.FindDescriptorByName(name)
		return desc.(protoreflect.MessageDescriptor)
	}
	return &openShellProtoTypes{
		providerProfile:                              message("openshell.v1.ProviderProfile"),
		providerProfileCredential:                    message("openshell.v1.ProviderProfileCredential"),
		providerCredentialTokenGrant:                 message("openshell.v1.ProviderCredentialTokenGrant"),
		providerCredentialTokenGrantSubjectToken:     message("openshell.v1.ProviderCredentialTokenGrantSubjectToken"),
		providerCredentialTokenGrantAudienceOverride: message("openshell.v1.ProviderCredentialTokenGrantAudienceOverride"),
		providerProfileImportItem:                    message("openshell.v1.ProviderProfileImportItem"),
		providerProfileDiagnostic:                    message("openshell.v1.ProviderProfileDiagnostic"),
		networkEndpoint:                              message("openshell.sandbox.v1.NetworkEndpoint"),
		networkBinary:                                message("openshell.sandbox.v1.NetworkBinary"),
		getProviderProfileRequest:                    message("openshell.v1.GetProviderProfileRequest"),
		providerProfileResponse:                      message("openshell.v1.ProviderProfileResponse"),
		importProviderProfilesRequest:                message("openshell.v1.ImportProviderProfilesRequest"),
		importProviderProfilesResponse:               message("openshell.v1.ImportProviderProfilesResponse"),
		updateProviderProfilesRequest:                message("openshell.v1.UpdateProviderProfilesRequest"),
		updateProviderProfilesResponse:               message("openshell.v1.UpdateProviderProfilesResponse"),
		lintProviderProfilesRequest:                  message("openshell.v1.LintProviderProfilesRequest"),
		lintProviderProfilesResponse:                 message("openshell.v1.LintProviderProfilesResponse"),
		deleteProviderProfileRequest:                 message("openshell.v1.DeleteProviderProfileRequest"),
		deleteProviderProfileResponse:                message("openshell.v1.DeleteProviderProfileResponse"),
		settingValue:                                 message("openshell.sandbox.v1.SettingValue"),
		updateConfigRequest:                          message("openshell.v1.UpdateConfigRequest"),
		updateConfigResponse:                         message("openshell.v1.UpdateConfigResponse"),
	}, nil
}

func openShellDescriptor() *descriptorpb.FileDescriptorSet {
	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		sandboxDescriptor(),
		openshellProviderDescriptor(),
	}}
}

func sandboxDescriptor() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:    proto.String("sandbox.proto"),
		Package: proto.String("openshell.sandbox.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("NetworkEndpoint"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("host", 1),
					uint32Field("port", 2),
					stringField("protocol", 3),
					stringField("tls", 4),
					stringField("enforcement", 5),
					stringField("access", 6),
					repeatedStringField("allowed_ips", 8),
				},
			},
			{
				Name:  proto.String("NetworkBinary"),
				Field: []*descriptorpb.FieldDescriptorProto{stringField("path", 1)},
			},
			{
				Name: proto.String("SettingValue"),
				Field: []*descriptorpb.FieldDescriptorProto{
					oneofStringField("string_value", 1, 0),
					oneofBoolField("bool_value", 2, 0),
					oneofInt64Field("int_value", 3, 0),
					oneofBytesField("bytes_value", 4, 0),
				},
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("value")}},
			},
		},
	}
}

func openshellProviderDescriptor() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:       proto.String("openshell_provider_profile.proto"),
		Package:    proto.String("openshell.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"sandbox.proto"},
		EnumType: []*descriptorpb.EnumDescriptorProto{
			{
				Name: proto.String("ProviderProfileCategory"),
				Value: []*descriptorpb.EnumValueDescriptorProto{
					enumValue("PROVIDER_PROFILE_CATEGORY_UNSPECIFIED", 0),
					enumValue("PROVIDER_PROFILE_CATEGORY_OTHER", 1),
					enumValue("PROVIDER_PROFILE_CATEGORY_INFERENCE", 2),
					enumValue("PROVIDER_PROFILE_CATEGORY_AGENT", 3),
					enumValue("PROVIDER_PROFILE_CATEGORY_SOURCE_CONTROL", 4),
					enumValue("PROVIDER_PROFILE_CATEGORY_MESSAGING", 5),
					enumValue("PROVIDER_PROFILE_CATEGORY_DATA", 6),
					enumValue("PROVIDER_PROFILE_CATEGORY_KNOWLEDGE", 7),
				},
			},
			{
				Name: proto.String("ProviderCredentialTokenGrantType"),
				Value: []*descriptorpb.EnumValueDescriptorProto{
					enumValue("PROVIDER_CREDENTIAL_TOKEN_GRANT_TYPE_UNSPECIFIED", 0),
					enumValue("PROVIDER_CREDENTIAL_TOKEN_GRANT_TYPE_CLIENT_CREDENTIALS", 1),
					enumValue("PROVIDER_CREDENTIAL_TOKEN_GRANT_TYPE_TOKEN_EXCHANGE", 2),
				},
			},
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("ProviderCredentialTokenGrantAudienceOverride"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("host", 1),
					uint32Field("port", 2),
					stringField("path", 3),
					stringField("audience", 4),
					repeatedStringField("scopes", 5),
				},
			},
			{
				Name: proto.String("ProviderCredentialTokenGrantSubjectToken"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("source", 1),
					stringField("credential", 2),
					stringField("subject_token_type", 3),
				},
			},
			{
				Name: proto.String("ProviderCredentialTokenGrant"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("token_endpoint", 1),
					stringField("audience", 2),
					repeatedStringField("scopes", 3),
					int64Field("cache_ttl_seconds", 4),
					repeatedMessageField("audience_overrides", 5, ".openshell.v1.ProviderCredentialTokenGrantAudienceOverride"),
					stringField("jwt_svid_audience", 6),
					stringField("client_assertion_type", 7),
					enumField("grant_type", 8, ".openshell.v1.ProviderCredentialTokenGrantType"),
					messageField("subject_token", 9, ".openshell.v1.ProviderCredentialTokenGrantSubjectToken"),
					stringField("requested_token_type", 10),
				},
			},
			{
				Name: proto.String("ProviderProfileCredential"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("name", 1),
					stringField("description", 2),
					repeatedStringField("env_vars", 3),
					boolField("required", 4),
					stringField("auth_style", 5),
					stringField("header_name", 6),
					stringField("query_param", 7),
					stringField("path_template", 9),
					messageField("token_grant", 10, ".openshell.v1.ProviderCredentialTokenGrant"),
				},
			},
			{
				Name: proto.String("ProviderProfile"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("id", 1),
					stringField("display_name", 2),
					stringField("description", 3),
					enumField("category", 4, ".openshell.v1.ProviderProfileCategory"),
					repeatedMessageField("credentials", 5, ".openshell.v1.ProviderProfileCredential"),
					repeatedMessageField("endpoints", 6, ".openshell.sandbox.v1.NetworkEndpoint"),
					repeatedMessageField("binaries", 7, ".openshell.sandbox.v1.NetworkBinary"),
					boolField("inference_capable", 8),
					uint64Field("resource_version", 10),
				},
			},
			{
				Name: proto.String("ProviderProfileImportItem"),
				Field: []*descriptorpb.FieldDescriptorProto{
					messageField("profile", 1, ".openshell.v1.ProviderProfile"),
					stringField("source", 2),
				},
			},
			{
				Name: proto.String("ProviderProfileDiagnostic"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("source", 1),
					stringField("profile_id", 2),
					stringField("field", 3),
					stringField("message", 4),
					stringField("severity", 5),
				},
			},
			{Name: proto.String("GetProviderProfileRequest"), Field: []*descriptorpb.FieldDescriptorProto{stringField("id", 1)}},
			{Name: proto.String("ProviderProfileResponse"), Field: []*descriptorpb.FieldDescriptorProto{messageField("profile", 1, ".openshell.v1.ProviderProfile")}},
			{Name: proto.String("ImportProviderProfilesRequest"), Field: []*descriptorpb.FieldDescriptorProto{repeatedMessageField("profiles", 1, ".openshell.v1.ProviderProfileImportItem")}},
			{
				Name: proto.String("ImportProviderProfilesResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					repeatedMessageField("diagnostics", 1, ".openshell.v1.ProviderProfileDiagnostic"),
					repeatedMessageField("profiles", 2, ".openshell.v1.ProviderProfile"),
					boolField("imported", 3),
				},
			},
			{
				Name: proto.String("UpdateProviderProfilesRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					messageField("profile", 1, ".openshell.v1.ProviderProfileImportItem"),
					uint64Field("expected_resource_version", 2),
					stringField("id", 3),
				},
			},
			{
				Name: proto.String("UpdateProviderProfilesResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					repeatedMessageField("diagnostics", 1, ".openshell.v1.ProviderProfileDiagnostic"),
					messageField("profile", 2, ".openshell.v1.ProviderProfile"),
					boolField("updated", 3),
				},
			},
			{Name: proto.String("LintProviderProfilesRequest"), Field: []*descriptorpb.FieldDescriptorProto{repeatedMessageField("profiles", 1, ".openshell.v1.ProviderProfileImportItem")}},
			{
				Name: proto.String("LintProviderProfilesResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					repeatedMessageField("diagnostics", 1, ".openshell.v1.ProviderProfileDiagnostic"),
					boolField("valid", 2),
				},
			},
			{Name: proto.String("DeleteProviderProfileRequest"), Field: []*descriptorpb.FieldDescriptorProto{stringField("id", 1)}},
			{Name: proto.String("DeleteProviderProfileResponse"), Field: []*descriptorpb.FieldDescriptorProto{boolField("deleted", 1)}},
			{
				Name: proto.String("UpdateConfigRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("name", 1),
					stringField("setting_key", 3),
					messageField("setting_value", 4, ".openshell.sandbox.v1.SettingValue"),
					boolField("delete_setting", 5),
					boolField("global", 6),
					uint64Field("expected_resource_version", 8),
					stringField("workspace", 10),
				},
			},
			{
				Name: proto.String("UpdateConfigResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					uint32Field("version", 1),
					stringField("policy_hash", 2),
					uint64Field("settings_revision", 3),
					boolField("deleted", 4),
				},
			},
		},
	}
}

func stringField(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return scalarField(name, number, descriptorpb.FieldDescriptorProto_TYPE_STRING)
}

func repeatedStringField(name string, number int32) *descriptorpb.FieldDescriptorProto {
	field := stringField(name, number)
	field.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	return field
}

func boolField(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return scalarField(name, number, descriptorpb.FieldDescriptorProto_TYPE_BOOL)
}

func bytesField(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return scalarField(name, number, descriptorpb.FieldDescriptorProto_TYPE_BYTES)
}

func uint32Field(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return scalarField(name, number, descriptorpb.FieldDescriptorProto_TYPE_UINT32)
}

func uint64Field(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return scalarField(name, number, descriptorpb.FieldDescriptorProto_TYPE_UINT64)
}

func int64Field(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return scalarField(name, number, descriptorpb.FieldDescriptorProto_TYPE_INT64)
}

func scalarField(name string, number int32, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   typ.Enum(),
	}
}

func oneofStringField(name string, number, oneofIndex int32) *descriptorpb.FieldDescriptorProto {
	return oneofField(stringField(name, number), oneofIndex)
}

func oneofBoolField(name string, number, oneofIndex int32) *descriptorpb.FieldDescriptorProto {
	return oneofField(boolField(name, number), oneofIndex)
}

func oneofInt64Field(name string, number, oneofIndex int32) *descriptorpb.FieldDescriptorProto {
	return oneofField(int64Field(name, number), oneofIndex)
}

func oneofBytesField(name string, number, oneofIndex int32) *descriptorpb.FieldDescriptorProto {
	return oneofField(bytesField(name, number), oneofIndex)
}

func oneofField(field *descriptorpb.FieldDescriptorProto, oneofIndex int32) *descriptorpb.FieldDescriptorProto {
	field.OneofIndex = proto.Int32(oneofIndex)
	return field
}

func messageField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(typeName),
	}
}

func repeatedMessageField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	field := messageField(name, number, typeName)
	field.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	return field
}

func enumField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
		TypeName: proto.String(typeName),
	}
}

func enumValue(name string, number int32) *descriptorpb.EnumValueDescriptorProto {
	return &descriptorpb.EnumValueDescriptorProto{Name: proto.String(name), Number: proto.Int32(number)}
}

func providerProfileCategoryNumber(category string) protoreflect.EnumNumber {
	switch category {
	case "inference":
		return 2
	case "agent":
		return 3
	case "source_control":
		return 4
	case "messaging":
		return 5
	case "data":
		return 6
	case "knowledge":
		return 7
	case "other":
		return 1
	default:
		return 0
	}
}
