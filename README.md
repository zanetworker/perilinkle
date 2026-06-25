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
      tls: none
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

Default sandbox SPIFFE ID:

```text
spiffe://<trust-domain>/<namespace>/sandbox/<sandbox-id>
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
      tls: none
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

## Development

```sh
go test ./...
make image-build
make deploy
make undeploy
```

`make deploy` applies the dev overlay: controller, ServiceGroup CRD, Keycloak,
and sample users.
