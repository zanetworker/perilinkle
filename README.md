# perilinkle

This is an experimental project to make it easy to securely access
backend systems from agents running in OpenShell managed sandboxes.

It is a Go Kubernetes controller that watches OpenShell-managed
sandboxes and `ServiceGroup` resources, ensuring that Keycloak is
appropriately configured to allow injection of SPIFFE-backed tokens
and the necessary OpenShell provider profiles in sync.

## Inputs

OpenShell creates `Sandbox` resources:

```yaml
apiVersion: agents.x-k8s.io/v1alpha1
kind: Sandbox
metadata:
  labels:
    openshell.ai/managed-by: openshell
    openshell.ai/sandbox-id: 6d795f7a-f916-4143-af7a-0995dabc0c7e
```

Service providers create `ServiceGroup` resources:

```yaml
apiVersion: perilinkle.dev/v1alpha1
kind: ServiceGroup
metadata:
  name: dummy-services
  namespace: openshell
spec:
  providerProfile:
    enabled: true
    id: keycloak-exchange
    displayName: Keycloak protected services
    endpointDefaults:
      port: 80
      protocol: rest
      access: read-write
  targetServices:
    - name: alpha
      namespace: default
      serviceAccountName: alpha
    - name: beta
      namespace: default
      serviceAccountName: beta
```

A `ServiceGroup` is namespaced, but the reconciled Keycloak realm is shared.
Sandbox IDs are assumed to be globally unique.

## Keycloak

Default realm: `openshell`.

Default sandbox SPIFFE ID (matches the OpenShell Helm SPIRE overlay; change it
with `--sandbox-spiffe-id-template`):

```text
spiffe://<trust-domain>/openshell/sandbox/<sandbox-id>
```

Default target-service SPIFFE ID:

```text
spiffe://<trust-domain>/ns/<namespace>/sa/<serviceAccountName>
```

Target services may override the SPIFFE ID with `spec.targetServices[].spiffeID`.

## Provider Profiles

When `spec.providerProfile.enabled` is true, perilinkle builds an OpenShell
provider profile from the `ServiceGroup` and upserts it via the Gateway API.

Per-service endpoint overrides:

```yaml
targetServices:
  - name: beta
    namespace: default
    endpoint:
      host: beta.default.svc.cluster.local
      port: 80
      protocol: rest
```

Per-service token overrides:

```yaml
targetServices:
  - name: beta
    namespace: default
    token:
      audience: beta
      scopes: ["beta.read"]
```

## Configuration

Important flags and matching environment variables:

- `--keycloak-url` / `KEYCLOAK_URL`
- `--keycloak-realm` / `KEYCLOAK_REALM`, default `openshell`
- `--keycloak-admin-password` / `KEYCLOAK_ADMIN_PASSWORD`
- `--spiffe-trust-domain` / `SPIFFE_TRUST_DOMAIN`
- `--gateway-client-id` / `GATEWAY_CLIENT_ID`, default `openshell-gateway`
- `--cli-client-id` / `CLI_CLIENT_ID`, default `openshell-cli`
- `--openshell-gateway-address` / `OPENSHELL_GATEWAY_ADDRESS`
- `--openshell-gateway-ca-file` / `OPENSHELL_GATEWAY_CA_FILE`
- `--gateway-api-client-id` / `GATEWAY_API_CLIENT_ID`, default
  `perilinkle-openshell-gateway`
- `--users-configmap` / `USERS_CONFIGMAP`, optional prototype users
- `--sandbox-api-version` / `SANDBOX_API_VERSION`, Agent Sandbox `Sandbox`
  API version to watch. Empty auto-detects through discovery, preferring
  `v1beta1` (Red Hat build of Agent Sandbox) and falling back to `v1alpha1`
- `--sandbox-spiffe-id-template` / `SANDBOX_SPIFFE_ID_TEMPLATE`, default
  `{trustDomain}/openshell/sandbox/{sandboxID}`. Must match the
  ClusterSPIFFEID that issues supervisor SVIDs, because Keycloak matches the
  SVID subject exactly. Placeholders: `{trustDomain}`, `{namespace}`, `{name}`,
  `{sandboxID}` (required)
- `--keycloak-admin-client-id` / `KEYCLOAK_ADMIN_CLIENT_ID` and
  `--keycloak-admin-client-secret` / `KEYCLOAK_ADMIN_CLIENT_SECRET`, optional
  service-account client (`client_credentials`) with realm-management roles,
  used instead of the admin user password

## Development

```sh
go test ./...
make image-build
make deploy
make undeploy
```

`make deploy` applies the dev overlay: controller, ServiceGroup CRD, Keycloak,
and sample users.
