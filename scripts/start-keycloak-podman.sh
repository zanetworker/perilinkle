#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
KEYCLOAK_MANIFEST="${KEYCLOAK_MANIFEST:-${PROJECT_ROOT}/config/dev/keycloak.yaml}"

run() {
    printf '\n$ %s\n' "$*" >&2
    "$@"
}

require_cmd() {
    local cmd="$1"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        printf 'missing required command: %s\n' "$cmd" >&2
        exit 1
    fi
}

manifest_env() {
    local name="$1"
    awk -v key="$name" '
        $0 ~ "- name: " key "$" { found=1; next }
        found && /^[[:space:]]*value:/ {
            sub(/^[[:space:]]*value:[[:space:]]*/, "")
            gsub(/^"|"$/, "")
            print
            exit
        }
    ' "$KEYCLOAK_MANIFEST"
}

manifest_secret_password() {
    awk '
        /^stringData:/ { in_string_data=1; next }
        in_string_data && /^[^[:space:]]/ { in_string_data=0 }
        in_string_data && /^[[:space:]]*password:/ {
            sub(/^[[:space:]]*password:[[:space:]]*/, "")
            gsub(/^"|"$/, "")
            print
            exit
        }
    ' "$KEYCLOAK_MANIFEST"
}

manifest_container_image() {
    local name="$1"
    awk -v container="$name" '
        $0 ~ "- name: " container "$" { found=1; next }
        found && /^[[:space:]]*image:/ {
            sub(/^[[:space:]]*image:[[:space:]]*/, "")
            print
            exit
        }
    ' "$KEYCLOAK_MANIFEST"
}

manifest_container_args() {
    local name="$1"
    awk -v container="$name" '
        $0 ~ "- name: " container "$" { found=1; next }
        found && /^[[:space:]]*args:/ {
            sub(/^[[:space:]]*args:[[:space:]]*/, "")
            gsub(/\[/, "")
            gsub(/\]/, "")
            gsub(/,/, " ")
            gsub(/"/, "")
            print
            exit
        }
    ' "$KEYCLOAK_MANIFEST"
}

quote_env_value() {
    printf '%q' "$1"
}

write_env_line() {
    local name="$1"
    local value="$2"
    if [[ -n "$KEYCLOAK_ENV_FILE" ]]; then
        printf '%s=%s\n' "$name" "$(quote_env_value "$value")" >>"$KEYCLOAK_ENV_FILE"
    fi
}

wait_for_file() {
    local path="$1"
    local label="$2"
    for _ in $(seq 1 120); do
        if [[ -s "$path" ]]; then
            return 0
        fi
        sleep 0.5
    done
    printf '%s was not created at %s\n' "$label" "$path" >&2
    return 1
}

wait_for_http() {
    local url="$1"
    local label="$2"
    local host_header="$3"
    for _ in $(seq 1 120); do
        if curl -fsS -H "Host: ${host_header}" "$url" >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.5
    done
    printf '%s did not become ready at %s\n' "$label" "$url" >&2
    return 1
}

hostname_header() {
    local hostname="$1"
    hostname="${hostname#http://}"
    hostname="${hostname#https://}"
    hostname="${hostname%%/*}"
    printf '%s\n' "$hostname"
}

cleanup_container() {
    podman rm -f "$1" >/dev/null 2>&1 || true
}

require_cmd podman
require_cmd awk
require_cmd curl

if [[ ! -f "$KEYCLOAK_MANIFEST" ]]; then
    printf 'Keycloak manifest not found: %s\n' "$KEYCLOAK_MANIFEST" >&2
    exit 1
fi

PODMAN_NETWORK="${PODMAN_NETWORK:-openshell}"
KEYCLOAK_CONTAINER="${KEYCLOAK_CONTAINER:-perilinkle-keycloak}"
SPIFFE_HELPER_CONTAINER="${SPIFFE_HELPER_CONTAINER:-perilinkle-keycloak-spiffe-helper}"
KEYCLOAK_SERVICE_ALIAS="${KEYCLOAK_SERVICE_ALIAS:-keycloak.openshell.svc.cluster.local}"
KEYCLOAK_IMAGE="${KEYCLOAK_IMAGE:-$(manifest_container_image keycloak)}"
SPIFFE_HELPER_IMAGE="${SPIFFE_HELPER_IMAGE:-$(manifest_container_image spiffe-helper)}"
KEYCLOAK_ARGS="${KEYCLOAK_ARGS:-$(manifest_container_args keycloak)}"
KEYCLOAK_PORT="${KEYCLOAK_PORT:-9090}"
KEYCLOAK_BIND_HOST="${KEYCLOAK_BIND_HOST:-127.0.0.1}"
KEYCLOAK_STATE_DIR="${KEYCLOAK_STATE_DIR:-$(mktemp -d -t perilinkle-keycloak.XXXXXX)}"
KEYCLOAK_ENV_FILE="${KEYCLOAK_ENV_FILE:-}"
CLEANUP_EXISTING="${CLEANUP_EXISTING:-1}"
START_SPIFFE_HELPER="${START_SPIFFE_HELPER:-1}"

KC_HOSTNAME="${KC_HOSTNAME:-$(manifest_env KC_HOSTNAME)}"
KC_HOSTNAME_STRICT="${KC_HOSTNAME_STRICT:-$(manifest_env KC_HOSTNAME_STRICT)}"
KC_BOOTSTRAP_ADMIN_USERNAME="${KC_BOOTSTRAP_ADMIN_USERNAME:-$(manifest_env KC_BOOTSTRAP_ADMIN_USERNAME)}"
KC_BOOTSTRAP_ADMIN_PASSWORD="${KC_BOOTSTRAP_ADMIN_PASSWORD:-$(manifest_secret_password)}"
KC_PROXY_HEADERS="${KC_PROXY_HEADERS:-$(manifest_env KC_PROXY_HEADERS)}"
KC_FEATURES="${KC_FEATURES:-$(manifest_env KC_FEATURES)}"
KC_TRUSTSTORE_PATHS="${KC_TRUSTSTORE_PATHS:-$(manifest_env KC_TRUSTSTORE_PATHS)}"

KEYCLOAK_IMAGE="${KEYCLOAK_IMAGE:-quay.io/keycloak/keycloak:26.6.0}"
SPIFFE_HELPER_IMAGE="${SPIFFE_HELPER_IMAGE:-ghcr.io/spiffe/spiffe-helper:0.11.0}"
KEYCLOAK_ARGS="${KEYCLOAK_ARGS:-start-dev}"
KC_HOSTNAME="${KC_HOSTNAME:-keycloak.127.0.0.1.sslip.io}"
KC_HOSTNAME_STRICT="${KC_HOSTNAME_STRICT:-true}"
KC_BOOTSTRAP_ADMIN_USERNAME="${KC_BOOTSTRAP_ADMIN_USERNAME:-admin}"
KC_BOOTSTRAP_ADMIN_PASSWORD="${KC_BOOTSTRAP_ADMIN_PASSWORD:-admin}"
KC_PROXY_HEADERS="${KC_PROXY_HEADERS:-xforwarded}"
KC_FEATURES="${KC_FEATURES:-client-auth-federated,spiffe}"
KC_TRUSTSTORE_PATHS="${KC_TRUSTSTORE_PATHS:-/etc/x509/spiffe-bundle/bundle.pem}"

bundle_dir="${KEYCLOAK_STATE_DIR}/spiffe-bundle"
helper_config_dir="${KEYCLOAK_STATE_DIR}/spiffe-helper"
helper_config="${helper_config_dir}/config.conf"

mkdir -p "$bundle_dir" "$helper_config_dir"
chmod 0777 "$KEYCLOAK_STATE_DIR" "$bundle_dir" "$helper_config_dir"

cat >"$helper_config" <<'EOF_HELPER'
agent_address = "/run/spire/sockets/spire-agent.sock"
cmd = ""
cert_dir = "/target"
renew_signal = "SIGHUP"
svid_file_name = "svid.pem"
svid_key_file_name = "key.pem"
svid_bundle_file_name = "bundle.pem"
EOF_HELPER
chmod 0644 "$helper_config"

if ! podman network exists "$PODMAN_NETWORK" >/dev/null 2>&1; then
    run podman network create "$PODMAN_NETWORK"
fi

if [[ "$CLEANUP_EXISTING" == "1" ]]; then
    cleanup_container "$KEYCLOAK_CONTAINER"
    cleanup_container "$SPIFFE_HELPER_CONTAINER"
fi

if [[ -n "$KEYCLOAK_ENV_FILE" ]]; then
    mkdir -p "$(dirname "$KEYCLOAK_ENV_FILE")"
    : >"$KEYCLOAK_ENV_FILE"
fi

if [[ "$START_SPIFFE_HELPER" == "1" ]]; then
    if [[ -z "${SPIFFE_AGENT_SOCKET_HOST_PATH:-}" ]]; then
        printf 'SPIFFE_AGENT_SOCKET_HOST_PATH is required when START_SPIFFE_HELPER=1\n' >&2
        exit 1
    fi
    if [[ ! -S "$SPIFFE_AGENT_SOCKET_HOST_PATH" ]]; then
        printf 'SPIRE agent Workload API socket does not exist: %s\n' "$SPIFFE_AGENT_SOCKET_HOST_PATH" >&2
        exit 1
    fi

    run podman run -d \
        --name "$SPIFFE_HELPER_CONTAINER" \
        --network "$PODMAN_NETWORK" \
        --label app=keycloak \
        --security-opt label=disable \
        -v "${SPIFFE_AGENT_SOCKET_HOST_PATH}:/run/spire/sockets/spire-agent.sock" \
        -v "${bundle_dir}:/target:z" \
        -v "${helper_config_dir}:/etc/spiffe-helper:ro,z" \
        "$SPIFFE_HELPER_IMAGE" \
        -config /etc/spiffe-helper/config.conf

    if ! wait_for_file "${bundle_dir}/bundle.pem" "SPIFFE bundle"; then
        podman logs "$SPIFFE_HELPER_CONTAINER" >&2 || true
        exit 1
    fi
else
    if [[ ! -s "${bundle_dir}/bundle.pem" ]]; then
        printf 'START_SPIFFE_HELPER=0 requires an existing bundle at %s\n' "${bundle_dir}/bundle.pem" >&2
        exit 1
    fi
fi

read -r -a keycloak_args <<<"$KEYCLOAK_ARGS"
run podman run -d \
    --name "$KEYCLOAK_CONTAINER" \
    --network "$PODMAN_NETWORK" \
    --network-alias keycloak \
    --network-alias "$KEYCLOAK_SERVICE_ALIAS" \
    --label app=keycloak \
    -p "${KEYCLOAK_BIND_HOST}:${KEYCLOAK_PORT}:8080" \
    -v "${bundle_dir}:/etc/x509/spiffe-bundle:ro,z" \
    -e "KC_HOSTNAME=${KC_HOSTNAME}" \
    -e "KC_HOSTNAME_STRICT=${KC_HOSTNAME_STRICT}" \
    -e "KC_BOOTSTRAP_ADMIN_USERNAME=${KC_BOOTSTRAP_ADMIN_USERNAME}" \
    -e "KC_BOOTSTRAP_ADMIN_PASSWORD=${KC_BOOTSTRAP_ADMIN_PASSWORD}" \
    -e "KC_PROXY_HEADERS=${KC_PROXY_HEADERS}" \
    -e "KC_FEATURES=${KC_FEATURES}" \
    -e "KC_TRUSTSTORE_PATHS=${KC_TRUSTSTORE_PATHS}" \
    "$KEYCLOAK_IMAGE" \
    "${keycloak_args[@]}"

if ! wait_for_http "http://${KEYCLOAK_BIND_HOST}:${KEYCLOAK_PORT}/realms/master" "Keycloak" "$(hostname_header "$KC_HOSTNAME")"; then
    podman logs "$KEYCLOAK_CONTAINER" >&2 || true
    exit 1
fi

KEYCLOAK_URL="${KEYCLOAK_URL:-http://${KEYCLOAK_BIND_HOST}:${KEYCLOAK_PORT}}"
write_env_line KEYCLOAK_URL "$KEYCLOAK_URL"
write_env_line KEYCLOAK_ADMIN_PASSWORD "$KC_BOOTSTRAP_ADMIN_PASSWORD"
write_env_line KEYCLOAK_CONTAINER "$KEYCLOAK_CONTAINER"
write_env_line KEYCLOAK_SERVICE_ALIAS "$KEYCLOAK_SERVICE_ALIAS"
write_env_line SPIFFE_HELPER_CONTAINER "$SPIFFE_HELPER_CONTAINER"
write_env_line KEYCLOAK_STATE_DIR "$KEYCLOAK_STATE_DIR"
write_env_line KEYCLOAK_SPIFFE_BUNDLE_DIR "$bundle_dir"
write_env_line KC_HOSTNAME "$KC_HOSTNAME"

printf 'Keycloak container: %s\n' "$KEYCLOAK_CONTAINER"
printf 'Keycloak URL for local clients: %s\n' "$KEYCLOAK_URL"
printf 'Keycloak advertised hostname: %s\n' "$KC_HOSTNAME"
printf 'Keycloak service alias: %s\n' "$KEYCLOAK_SERVICE_ALIAS"
printf 'Keycloak admin username: %s\n' "$KC_BOOTSTRAP_ADMIN_USERNAME"
printf 'Keycloak admin password: %s\n' "$KC_BOOTSTRAP_ADMIN_PASSWORD"
printf 'SPIFFE bundle dir: %s\n' "$bundle_dir"
if [[ "$START_SPIFFE_HELPER" == "1" ]]; then
    printf 'SPIFFE helper container: %s\n' "$SPIFFE_HELPER_CONTAINER"
fi
if [[ -n "$KEYCLOAK_ENV_FILE" ]]; then
    printf 'Wrote environment file: %s\n' "$KEYCLOAK_ENV_FILE"
fi
