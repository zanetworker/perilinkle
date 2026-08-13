#!/usr/bin/env bash

set -euo pipefail

# Stop/remove the local Podman components that make up the SPIFFE-enabled
# OpenShell gateway stack. Defaults match the OpenShell Podman demo scripts.

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

source_env_file() {
    local path="$1"
    if [[ -n "$path" ]]; then
        if [[ ! -f "$path" ]]; then
            printf 'environment file not found: %s\n' "$path" >&2
            exit 1
        fi
        # shellcheck disable=SC1090
        source "$path"
    fi
}

container_exists() {
    podman container exists "$1" >/dev/null 2>&1
}

stop_container() {
    local name="$1"
    if [[ -z "$name" ]]; then
        return 0
    fi
    if ! container_exists "$name"; then
        printf 'Container not found, skipping: %s\n' "$name" >&2
        return 0
    fi

    if [[ "$REMOVE_CONTAINERS" == "1" ]]; then
        run podman rm -f -t "$STOP_TIMEOUT_SECS" "$name"
    else
        run podman stop -t "$STOP_TIMEOUT_SECS" "$name"
    fi
}

require_cmd podman

source_env_file "${SPIRE_SERVER_ENV_FILE:-}"
source_env_file "${SPIRE_AGENT_ENV_FILE:-}"
source_env_file "${KEYCLOAK_ENV_FILE:-}"
source_env_file "${GATEWAY_ENV_FILE:-}"

GATEWAY_CONTAINER="${GATEWAY_CONTAINER:-perilinkle-gateway}"
KEYCLOAK_CONTAINER="${KEYCLOAK_CONTAINER:-perilinkle-keycloak}"
SPIFFE_HELPER_CONTAINER="${SPIFFE_HELPER_CONTAINER:-perilinkle-keycloak-spiffe-helper}"
SPIRE_AGENT_CONTAINER="${SPIRE_AGENT_CONTAINER:-perilinkle-spire-agent}"
SPIRE_OIDC_CONTAINER="${SPIRE_OIDC_CONTAINER:-perilinkle-spire-oidc}"
SPIRE_SERVER_CONTAINER="${SPIRE_SERVER_CONTAINER:-perilinkle-spire-server}"
STOP_TIMEOUT_SECS="${STOP_TIMEOUT_SECS:-5}"
REMOVE_CONTAINERS="${REMOVE_CONTAINERS:-1}"

if [[ ! "$STOP_TIMEOUT_SECS" =~ ^[0-9]+$ ]]; then
    printf 'STOP_TIMEOUT_SECS must be a non-negative integer, got: %s\n' "$STOP_TIMEOUT_SECS" >&2
    exit 1
fi
if [[ "$REMOVE_CONTAINERS" != "0" && "$REMOVE_CONTAINERS" != "1" ]]; then
    printf 'REMOVE_CONTAINERS must be 0 or 1, got: %s\n' "$REMOVE_CONTAINERS" >&2
    exit 1
fi

# Shut down consumers before providers.
if [[ -n "${PERILINKLE_PODMAN_PID:-}" ]]; then
    if kill -0 "$PERILINKLE_PODMAN_PID" >/dev/null 2>&1; then
        run kill "$PERILINKLE_PODMAN_PID"
    fi
fi

stop_container "$GATEWAY_CONTAINER"
stop_container "$KEYCLOAK_CONTAINER"
stop_container "$SPIFFE_HELPER_CONTAINER"
stop_container "$SPIRE_AGENT_CONTAINER"
stop_container "$SPIRE_OIDC_CONTAINER"
stop_container "$SPIRE_SERVER_CONTAINER"

if [[ -n "${PODMAN_SERVICE_PID:-}" ]]; then
    if kill -0 "$PODMAN_SERVICE_PID" >/dev/null 2>&1; then
        run kill "$PODMAN_SERVICE_PID"
    fi
fi

printf '\nPodman SPIFFE gateway stack cleanup complete.\n'
