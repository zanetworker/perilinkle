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

## Podman

`cmd/perilinkle-podman` provides a local Podman-oriented reconciler for the same
Keycloak model. It reads optional `ServiceGroup` YAML files, discovers local
OpenShell containers from Podman labels, configures Keycloak for the gateway,
target services, and sandbox clients, and can register the gateway and sandbox
containers as SPIRE workload entries.

Expected gateway labels:

```sh
--label openshell.managed=true
--label openshell.ai/role=gateway
--label openshell.ai/gateway-id=dev-gateway
```

Expected sandbox labels:

```sh
--label openshell.managed=true
--label openshell.ai/sandbox-id=<sandbox-id>
--label openshell.ai/namespace=<namespace>
```

Expected Keycloak label:

```sh
--label app=keycloak
```

The `scripts/start-keycloak-podman.sh` launcher applies this label to both the
Keycloak container and its `spiffe-helper` container. The helper is the process
that calls the SPIRE Workload API and writes SVID material for Keycloak.
`perilinkle-podman` uses `KEYCLOAK_CONTAINER`, defaulting to
`perilinkle-keycloak`, to derive the default Keycloak SPIFFE ID:

```text
spiffe://<trust-domain>/podman/keycloak/<keycloak-container>
```

Example:

```sh
scripts/start-podman-stack.sh
```

To run only the reconciler against an already-started local stack:

```sh
go run ./cmd/perilinkle-podman \
  --watch \
  --keycloak-url http://localhost:8080 \
  --keycloak-admin-password "$KEYCLOAK_ADMIN_PASSWORD" \
  --gateway-api-client-id perilinkle-openshell-gateway \
  --openshell-gateway-address 127.0.0.1:8888 \
  --enable-providers-v2 \
  --spire-parent-id spiffe://openshell.local/spire/agent/join_token/local \
  --service-group ./sample_servicegroup.yaml
```

`--enable-providers-v2` sets the OpenShell gateway-global runtime setting
`providers_v2_enabled=true`. It requires `--openshell-gateway-address` and a
Keycloak confidential client from `--gateway-api-client-id`.

If `--gateway-spiffe-subject` is not set, it is derived from the discovered
gateway as:

```text
spiffe://<trust-domain>/podman/gateway/<gateway-id>
```

SPIRE registration uses Docker/Podman workload selectors based on the labels
above, for example:

```text
docker:label:app:keycloak
docker:label:openshell.ai/sandbox-id:<sandbox-id>
```

The local SPIRE agent must be configured with a workload attestor that can emit
Docker/Podman label selectors. The reconciler creates registration entries via
`spire-server entry`; the workload still obtains SVIDs from the local agent
Workload API.
