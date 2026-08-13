#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

run() {
    printf "\n$ %s\n" "$*" >&2
    "$@"
}

require_cmd() {
    local cmd="$1"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        printf "missing required command: %s\n" "$cmd" >&2
        exit 1
    fi
}

source_env_file() {
    local path="$1"
    if [[ ! -f "$path" ]]; then
        printf "environment file not found: %s\n" "$path" >&2
        exit 1
    fi
    # shellcheck disable=SC1090
    source "$path"
}

controller_command() {
    if [[ -n "${PERILINKLE_PODMAN:-}" ]]; then
        printf "%s\n" "$PERILINKLE_PODMAN"
        return
    fi
    if [[ -x "${PROJECT_ROOT}/perilinkle-podman" ]]; then
        printf "%s\n" "${PROJECT_ROOT}/perilinkle-podman"
        return
    fi
    printf "go run ./cmd/perilinkle-podman\n"
}

podman_network_gateway() {
    local gateway
    gateway="$(
        podman network inspect "$PODMAN_NETWORK" 2>/dev/null |
            awk -F'"' '/"gateway"[[:space:]]*:/ { print $4; exit }'
    )"
    if [[ -z "$gateway" ]]; then
        printf "could not determine gateway IP for Podman network %s\n" "$PODMAN_NETWORK" >&2
        exit 1
    fi
    printf "%s\n" "$gateway"
}

register_openshell_gateway() {
    if [[ "${REGISTER_OPENSHELL_GATEWAY:-1}" != "1" ]]; then
        return
    fi
    if ! command -v openshell >/dev/null 2>&1; then
        printf "\nopenshell CLI not found; register the gateway manually:\n" >&2
        printf "  openshell gateway add %s --name %s --oidc-issuer %s --oidc-client-id %s --oidc-audience %s\n" \
            "$GATEWAY_ENDPOINT" "$GATEWAY_CLI_NAME" "$GATEWAY_OIDC_ISSUER" "$CLI_CLIENT_ID" "$CLI_CLIENT_ID" >&2
        return
    fi

    openshell gateway remove "$GATEWAY_CLI_NAME" >/dev/null 2>&1 || true
    run openshell gateway add "$GATEWAY_ENDPOINT" \
        --name "$GATEWAY_CLI_NAME" \
        --oidc-issuer "$GATEWAY_OIDC_ISSUER" \
        --oidc-client-id "$CLI_CLIENT_ID" \
        --oidc-audience "$CLI_CLIENT_ID"
    run openshell gateway select "$GATEWAY_CLI_NAME"
}

start_perilinkle_podman_daemon() {
    PERILINKLE_PODMAN_LOG="${PERILINKLE_PODMAN_LOG:-${STACK_STATE_DIR}/perilinkle-podman.log}"
    printf "\nStarting perilinkle-podman daemon; log: %s\n" "$PERILINKLE_PODMAN_LOG" >&2
    (
        cd "$PROJECT_ROOT"
        env \
            KEYCLOAK_URL="$KEYCLOAK_URL" \
            KEYCLOAK_REALM="$KEYCLOAK_REALM" \
            KEYCLOAK_ADMIN_PASSWORD="$KEYCLOAK_ADMIN_PASSWORD" \
            SPIFFE_TRUST_DOMAIN="$SPIFFE_TRUST_DOMAIN" \
            SPIFFE_BUNDLE_ENDPOINT="$SPIFFE_BUNDLE_ENDPOINT" \
            SPIRE_PARENT_ID="$SPIRE_AGENT_PARENT_ID" \
            SPIRE_SERVER_CONTAINER="$SPIRE_SERVER_CONTAINER" \
            SPIRE_SERVER_SOCKET="$SPIRE_SERVER_SOCKET" \
            GATEWAY_API_CLIENT_ID="$GATEWAY_API_CLIENT_ID" \
            GATEWAY_SPIFFE_SUBJECT="$GATEWAY_SPIFFE_SUBJECT" \
            GATEWAY_CONTAINER="$GATEWAY_CONTAINER" \
            KEYCLOAK_CONTAINER="$KEYCLOAK_CONTAINER" \
            CLI_CLIENT_ID="$CLI_CLIENT_ID" \
            USERS_FILE="$USERS_FILE" \
            OPENSHELL_GATEWAY_ADDRESS="$OPENSHELL_GATEWAY_ADDRESS" \
            OPENSHELL_GATEWAY_TLS=false \
            ENABLE_PROVIDERS_V2="$ENABLE_PROVIDERS_V2" \
            "${perilinkle_podman_cmd[@]}" \
            --watch \
            --sync-interval "$PERILINKLE_PODMAN_SYNC_INTERVAL" \
            "${service_group_args[@]}"
    ) >"$PERILINKLE_PODMAN_LOG" 2>&1 &
    PERILINKLE_PODMAN_PID="$!"
    sleep 1
    if ! kill -0 "$PERILINKLE_PODMAN_PID" >/dev/null 2>&1; then
        printf "perilinkle-podman daemon exited during startup; log follows:\n" >&2
        cat "$PERILINKLE_PODMAN_LOG" >&2 || true
        exit 1
    fi
    printf "PERILINKLE_PODMAN_PID=%s\n" "$PERILINKLE_PODMAN_PID" >>"$GATEWAY_ENV_FILE"
    printf "PERILINKLE_PODMAN_LOG=%q\n" "$PERILINKLE_PODMAN_LOG" >>"$GATEWAY_ENV_FILE"
}

require_cmd podman
require_cmd curl

if [[ ! -x "${PROJECT_ROOT}/perilinkle-podman" && -z "${PERILINKLE_PODMAN:-}" ]]; then
    require_cmd go
fi

STACK_STATE_DIR="${STACK_STATE_DIR:-$(mktemp -d -t perilinkle-podman-stack.XXXXXX)}"
SPIRE_STATE_DIR="${SPIRE_STATE_DIR:-${STACK_STATE_DIR}/spire}"
SPIRE_SERVER_ENV_FILE="${SPIRE_SERVER_ENV_FILE:-${STACK_STATE_DIR}/spire-server.env}"
SPIRE_AGENT_ENV_FILE="${SPIRE_AGENT_ENV_FILE:-${STACK_STATE_DIR}/spire-agent.env}"
KEYCLOAK_ENV_FILE="${KEYCLOAK_ENV_FILE:-${STACK_STATE_DIR}/keycloak.env}"
GATEWAY_ENV_FILE="${GATEWAY_ENV_FILE:-${STACK_STATE_DIR}/gateway.env}"
KEYCLOAK_REALM="${KEYCLOAK_REALM:-openshell}"
KEYCLOAK_PORT="${KEYCLOAK_PORT:-9090}"
KEYCLOAK_OIDC_HOST="${KEYCLOAK_OIDC_HOST:-keycloak.127.0.0.1.sslip.io}"
CLI_CLIENT_ID="${CLI_CLIENT_ID:-openshell-cli}"
GATEWAY_ID="${GATEWAY_ID:-perilinkle-podman}"
GATEWAY_CLI_NAME="${GATEWAY_CLI_NAME:-podman}"
GATEWAY_IMAGE="${GATEWAY_IMAGE:-quay.io/gordons/openshell-gateway:token-exchange-4}"
SUPERVISOR_IMAGE="${SUPERVISOR_IMAGE:-quay.io/gordons/openshell-supervisor:token-exchange-4}"
GATEWAY_API_CLIENT_ID="${GATEWAY_API_CLIENT_ID:-perilinkle-openshell-gateway}"
ENABLE_PROVIDERS_V2="${ENABLE_PROVIDERS_V2:-1}"
PODMAN_NETWORK="${PODMAN_NETWORK:-openshell}"
TRUST_DOMAIN="${TRUST_DOMAIN:-openshell.local}"
SPIFFE_TRUST_DOMAIN="${SPIFFE_TRUST_DOMAIN:-spiffe://${TRUST_DOMAIN}}"
SPIFFE_BUNDLE_ENDPOINT="${SPIFFE_BUNDLE_ENDPOINT:-http://spire-oidc:8080/keys}"
SPIRE_AGENT_PARENT_ID="${SPIRE_AGENT_PARENT_ID:-spiffe://${TRUST_DOMAIN}/openshell/spire-agent/perilinkle}"
SPIRE_SERVER_CONTAINER="${SPIRE_SERVER_CONTAINER:-perilinkle-spire-server}"
SPIRE_SERVER_SOCKET="${SPIRE_SERVER_SOCKET:-/run/spire/server/private/api.sock}"
SERVICE_GROUPS="${SERVICE_GROUPS:-${PROJECT_ROOT}/sample_servicegroup.yaml}"
USERS_FILE="${USERS_FILE:-${PROJECT_ROOT}/config/with-users/users.yaml}"
GATEWAY_SPIFFE_SUBJECT="${GATEWAY_SPIFFE_SUBJECT:-${SPIFFE_TRUST_DOMAIN}/podman/gateway/${GATEWAY_ID}}"
PERILINKLE_PODMAN_SYNC_INTERVAL="${PERILINKLE_PODMAN_SYNC_INTERVAL:-10s}"

mkdir -p "$STACK_STATE_DIR"

printf "Podman stack state directory: %s\n" "$STACK_STATE_DIR"

run env \
    SPIRE_STATE_DIR="$SPIRE_STATE_DIR" \
    SPIRE_ENV_FILE="$SPIRE_SERVER_ENV_FILE" \
    TRUST_DOMAIN="$TRUST_DOMAIN" \
    "${SCRIPT_DIR}/spire/start-server-oidc.sh"
source_env_file "$SPIRE_SERVER_ENV_FILE"

PODMAN_NETWORK_GATEWAY="$(podman_network_gateway)"
GATEWAY_OIDC_ISSUER="${GATEWAY_OIDC_ISSUER:-http://${KEYCLOAK_OIDC_HOST}:${KEYCLOAK_PORT}/realms/${KEYCLOAK_REALM}}"
GATEWAY_ADD_HOSTS="${GATEWAY_ADD_HOSTS:-${KEYCLOAK_OIDC_HOST}:${PODMAN_NETWORK_GATEWAY}}"
KEYCLOAK_INTERNAL_BASE_URL="${GATEWAY_OIDC_ISSUER%/realms/${KEYCLOAK_REALM}}"
KEYCLOAK_BIND_HOST="${KEYCLOAK_BIND_HOST:-0.0.0.0}"
KEYCLOAK_URL="${KEYCLOAK_URL:-http://127.0.0.1:${KEYCLOAK_PORT}}"

run env \
    SPIRE_STATE_DIR="$SPIRE_STATE_DIR" \
    SPIRE_ENV_FILE="$SPIRE_AGENT_ENV_FILE" \
    TRUST_DOMAIN="$TRUST_DOMAIN" \
    SPIRE_AGENT_PARENT_ID="$SPIRE_AGENT_PARENT_ID" \
    "${SCRIPT_DIR}/spire/start-agent.sh"
source_env_file "$SPIRE_AGENT_ENV_FILE"

# Keycloak's spiffe-helper needs this entry before it can fetch the bundle used
# by Keycloak's SPIFFE client-auth feature. perilinkle-podman re-ensures the
# same entry later with the rest of the local workload entries.
run env \
    TRUST_DOMAIN="$TRUST_DOMAIN" \
    SPIRE_AGENT_PARENT_ID="$SPIRE_AGENT_PARENT_ID" \
    SPIRE_SERVER_CONTAINER="$SPIRE_SERVER_CONTAINER" \
    SPIRE_SERVER_SOCKET="$SPIRE_SERVER_SOCKET" \
    "${SCRIPT_DIR}/spire/register-keycloak.sh"

run env \
    SPIFFE_AGENT_SOCKET_HOST_PATH="$SPIRE_AGENT_SOCKET_HOST_PATH" \
    KEYCLOAK_ENV_FILE="$KEYCLOAK_ENV_FILE" \
    KEYCLOAK_BIND_HOST="$KEYCLOAK_BIND_HOST" \
    KEYCLOAK_PORT="$KEYCLOAK_PORT" \
    KEYCLOAK_URL="$KEYCLOAK_URL" \
    KC_HOSTNAME="$KEYCLOAK_INTERNAL_BASE_URL" \
    KC_HOSTNAME_STRICT=true \
    TRUST_DOMAIN="$TRUST_DOMAIN" \
    "${SCRIPT_DIR}/start-keycloak-podman.sh"
source_env_file "$KEYCLOAK_ENV_FILE"

service_group_args=()
for service_group in $SERVICE_GROUPS; do
    service_group_args+=(--service-group "$service_group")
done

read -r -a perilinkle_podman_cmd <<<"$(controller_command)"
(
    cd "$PROJECT_ROOT"
    run env \
        KEYCLOAK_URL="$KEYCLOAK_URL" \
        KEYCLOAK_REALM="$KEYCLOAK_REALM" \
        KEYCLOAK_ADMIN_PASSWORD="$KEYCLOAK_ADMIN_PASSWORD" \
        SPIFFE_TRUST_DOMAIN="$SPIFFE_TRUST_DOMAIN" \
        SPIFFE_BUNDLE_ENDPOINT="$SPIFFE_BUNDLE_ENDPOINT" \
        GATEWAY_API_CLIENT_ID="$GATEWAY_API_CLIENT_ID" \
        GATEWAY_SPIFFE_SUBJECT="$GATEWAY_SPIFFE_SUBJECT" \
        CLI_CLIENT_ID="$CLI_CLIENT_ID" \
        USERS_FILE="$USERS_FILE" \
        "${perilinkle_podman_cmd[@]}" \
        --keycloak-only \
        --spire-register=false \
        "${service_group_args[@]}"
)

run env \
    SPIRE_AGENT_ENV_FILE="$SPIRE_AGENT_ENV_FILE" \
    GATEWAY_ENV_FILE="$GATEWAY_ENV_FILE" \
    GATEWAY_ID="$GATEWAY_ID" \
    GATEWAY_IMAGE="$GATEWAY_IMAGE" \
    SUPERVISOR_IMAGE="$SUPERVISOR_IMAGE" \
    GATEWAY_ADD_HOSTS="$GATEWAY_ADD_HOSTS" \
    GATEWAY_OIDC_ISSUER="$GATEWAY_OIDC_ISSUER" \
    GATEWAY_OIDC_AUDIENCE="$CLI_CLIENT_ID" \
    GATEWAY_OIDC_CLIENT_ID="$CLI_CLIENT_ID" \
    "${SCRIPT_DIR}/start-gateway-podman.sh"
source_env_file "$GATEWAY_ENV_FILE"

gateway_address="${GATEWAY_ENDPOINT#http://}"
gateway_address="${gateway_address#https://}"
OPENSHELL_GATEWAY_ADDRESS="${OPENSHELL_GATEWAY_ADDRESS:-$gateway_address}"

start_perilinkle_podman_daemon

register_openshell_gateway

cat <<EOF

Podman stack is ready.

State directory:              ${STACK_STATE_DIR}
SPIRE server env:             ${SPIRE_SERVER_ENV_FILE}
SPIRE agent env:              ${SPIRE_AGENT_ENV_FILE}
Keycloak env:                 ${KEYCLOAK_ENV_FILE}
Gateway env:                  ${GATEWAY_ENV_FILE}
Users file:                   ${USERS_FILE}
Keycloak URL:                 ${KEYCLOAK_URL}
Gateway endpoint:             ${GATEWAY_ENDPOINT}
OIDC issuer:                  ${GATEWAY_OIDC_ISSUER}
OpenShell CLI gateway name:   ${GATEWAY_CLI_NAME}
Gateway image:                ${GATEWAY_IMAGE}
Supervisor image:             ${SUPERVISOR_IMAGE}
Gateway API client ID:        ${GATEWAY_API_CLIENT_ID}
Providers v2 enabled:         ${ENABLE_PROVIDERS_V2}
perilinkle-podman PID:        ${PERILINKLE_PODMAN_PID}
perilinkle-podman log:        ${PERILINKLE_PODMAN_LOG}
SPIRE agent parent ID:        ${SPIRE_AGENT_PARENT_ID}
SPIRE agent Workload API:     ${SPIRE_AGENT_SOCKET_HOST_PATH}

Cleanup:
  SPIRE_SERVER_ENV_FILE=${SPIRE_SERVER_ENV_FILE} \\
  SPIRE_AGENT_ENV_FILE=${SPIRE_AGENT_ENV_FILE} \\
  KEYCLOAK_ENV_FILE=${KEYCLOAK_ENV_FILE} \\
  GATEWAY_ENV_FILE=${GATEWAY_ENV_FILE} \\
  ${SCRIPT_DIR}/cleanup-podman.sh
EOF
