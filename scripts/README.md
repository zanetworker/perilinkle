# Podman Development Scripts

These scripts start the local Podman version of the perilinkle SPIFFE/OIDC
development stack. They are intentionally split so each component can be
restarted independently while the flow is still experimental.

## All-In-One

To start the local SPIRE server/OIDC provider, SPIRE agent, Keycloak,
OpenShell gateway, and `perilinkle-podman` reconciler with the default sample
`ServiceGroup`:

```sh
"$PERILINKLE_REPO/scripts/start-podman-stack.sh"
```

Optional inputs:

```sh
SERVICE_GROUPS="./sample_servicegroup.yaml ./another-servicegroup.yaml" \
STACK_STATE_DIR=/tmp/perilinkle-podman-stack \
"$PERILINKLE_REPO/scripts/start-podman-stack.sh"
```

The default gateway and supervisor images are currently pinned to the SPIFFE
token-exchange builds:

```sh
GATEWAY_IMAGE=quay.io/gordons/openshell-gateway:token-exchange-4
SUPERVISOR_IMAGE=quay.io/gordons/openshell-supervisor:token-exchange-4
```

The wrapper writes component env files under `STACK_STATE_DIR` and prints a
matching cleanup command when startup completes.
For the all-in-one flow, Keycloak is advertised as
`http://keycloak.127.0.0.1.sslip.io:9090` so the host-side OpenShell
CLI/browser can reach the same OIDC issuer URL that the gateway validates. The
gateway container gets an `--add-host` override that maps that hostname to the
Podman network's host gateway IP. Override the host portion with
`KEYCLOAK_OIDC_HOST`, the full issuer with `GATEWAY_OIDC_ISSUER`, or the
container host mappings with `GATEWAY_ADD_HOSTS`.
The wrapper also runs `perilinkle-podman --keycloak-only` before starting the
gateway; this creates the `openshell` realm before the gateway attempts OIDC
discovery. After the gateway starts, the wrapper runs the full reconciler to
register gateway, Keycloak, and sandbox SPIRE entries.
By default it also replaces the local OpenShell CLI gateway entry named
`podman` with an OIDC-enabled registration. Override the name with
`GATEWAY_CLI_NAME` or set `REGISTER_OPENSHELL_GATEWAY=0` to skip CLI
registration.
The Podman reconciler loads demo users from
`config/with-users/users.yaml` by default. For login testing, use one of:

```text
admin-user / admin123
gordon / gordon123
regular-user / user123
```

Override the user manifest with `USERS_FILE`.
After the gateway starts, the wrapper runs `perilinkle-podman` as a background
polling daemon. It re-ensures Keycloak and SPIRE state every 10 seconds by
default, so newly-created sandbox containers are picked up automatically. Change
the interval with `PERILINKLE_PODMAN_SYNC_INTERVAL`; the daemon PID and log path
are written to the gateway env file for cleanup.
The wrapper also enables OpenShell provider profiles v2 by setting the
gateway-global runtime setting `providers_v2_enabled=true`. This is done through
the gateway API, not the gateway TOML file. Set `ENABLE_PROVIDERS_V2=0` to skip
that step, or override the service client with `GATEWAY_API_CLIENT_ID`.

## Startup Order

Use one shared SPIRE state directory so the server and agent env files point at
the same local runtime:

```sh
export PERILINKLE_REPO=/home/gsim/projects/experiments/perilinkle
export SPIRE_STATE_DIR="$(mktemp -d -t perilinkle-spire.XXXXXX)"
```

Start SPIRE server and OIDC discovery:

```sh
SPIRE_STATE_DIR="$SPIRE_STATE_DIR" \
SPIRE_ENV_FILE=/tmp/perilinkle-spire-server.env \
"$PERILINKLE_REPO/scripts/spire/start-server-oidc.sh"
```

Start the SPIRE agent. If no Podman API socket exists, this script starts a
temporary rootless `podman system service` and writes its socket and PID to the
env file:

```sh
SPIRE_STATE_DIR="$SPIRE_STATE_DIR" \
SPIRE_ENV_FILE=/tmp/perilinkle-spire-agent.env \
"$PERILINKLE_REPO/scripts/spire/start-agent.sh"

source /tmp/perilinkle-spire-agent.env
```

Register the Keycloak/spiffe-helper workload entry before starting Keycloak.
The helper container uses the `app=keycloak` selector so it can fetch the SPIFFE
bundle used by Keycloak's SPIFFE client-auth feature:

```sh
"$PERILINKLE_REPO/scripts/spire/register-keycloak.sh"
```

Start Keycloak:

```sh
SPIFFE_AGENT_SOCKET_HOST_PATH="$SPIRE_AGENT_SOCKET_HOST_PATH" \
KEYCLOAK_ENV_FILE=/tmp/perilinkle-keycloak.env \
"$PERILINKLE_REPO/scripts/start-keycloak-podman.sh"

source /tmp/perilinkle-keycloak.env
```

Start the OpenShell gateway. Point OIDC at Keycloak from the gateway container's
Podman network view:

```sh
SPIRE_AGENT_ENV_FILE=/tmp/perilinkle-spire-agent.env \
GATEWAY_ENV_FILE=/tmp/perilinkle-gateway.env \
GATEWAY_OIDC_ISSUER=http://keycloak:8080/realms/openshell \
GATEWAY_OIDC_AUDIENCE=openshell-cli \
GATEWAY_OIDC_CLIENT_ID=openshell-cli \
"$PERILINKLE_REPO/scripts/start-gateway-podman.sh"

source /tmp/perilinkle-gateway.env
```

Run the Podman reconciler to configure Keycloak and register gateway, Keycloak,
and sandbox SPIRE entries. Add `--watch` to keep it running and polling for new
sandbox containers. The SPIRE server is running in Podman, so point the
reconciler at that container and the in-container admin socket path:

```sh
KEYCLOAK_URL="$KEYCLOAK_URL" \
KEYCLOAK_ADMIN_PASSWORD="$KEYCLOAK_ADMIN_PASSWORD" \
GATEWAY_API_CLIENT_ID=perilinkle-openshell-gateway \
SPIRE_PARENT_ID="$SPIRE_AGENT_PARENT_ID" \
SPIRE_SERVER_SOCKET=/run/spire/server/private/api.sock \
SPIRE_SERVER_CONTAINER=perilinkle-spire-server \
OPENSHELL_GATEWAY_ADDRESS=127.0.0.1:8888 \
ENABLE_PROVIDERS_V2=1 \
go run ./cmd/perilinkle-podman \
  --watch \
  --service-group ./sample_servicegroup.yaml
```

Register individual sandboxes manually only when you are not using the
reconciler:

```sh
"$PERILINKLE_REPO/scripts/spire/register-sandbox.sh" "$SANDBOX_ID"
```

## Cleanup

```sh
SPIRE_AGENT_ENV_FILE=/tmp/perilinkle-spire-agent.env \
GATEWAY_ENV_FILE=/tmp/perilinkle-gateway.env \
"$PERILINKLE_REPO/scripts/cleanup-podman.sh"
```

The cleanup script removes the gateway, SPIRE agent, SPIRE OIDC provider, and
SPIRE server containers. If the agent script started a temporary Podman API
service, cleanup stops that process as well.
