#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

SPIRE_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PODMAN_NETWORK="${PODMAN_NETWORK:-openshell}"
TRUST_DOMAIN="${TRUST_DOMAIN:-openshell.local}"
SPIRE_AGENT_PARENT_ID="${SPIRE_AGENT_PARENT_ID:-spiffe://${TRUST_DOMAIN}/openshell/spire-agent/perilinkle}"
SPIRE_SERVER_IMAGE="${SPIRE_SERVER_IMAGE:-ghcr.io/spiffe/spire-server:1.12.4}"
SPIRE_AGENT_IMAGE="${SPIRE_AGENT_IMAGE:-ghcr.io/spiffe/spire-agent:1.12.4}"
SPIRE_OIDC_IMAGE="${SPIRE_OIDC_IMAGE:-ghcr.io/spiffe/oidc-discovery-provider:1.12.4}"
SPIRE_SERVER_CONTAINER="${SPIRE_SERVER_CONTAINER:-perilinkle-spire-server}"
SPIRE_AGENT_CONTAINER="${SPIRE_AGENT_CONTAINER:-perilinkle-spire-agent}"
SPIRE_OIDC_CONTAINER="${SPIRE_OIDC_CONTAINER:-perilinkle-spire-oidc}"
SPIRE_SERVER_BIN="${SPIRE_SERVER_BIN:-/opt/spire/bin/spire-server}"
OIDC_PORT="${OIDC_PORT:-18081}"
SPIRE_STATE_DIR="${SPIRE_STATE_DIR:-}"
SPIRE_ENV_FILE="${SPIRE_ENV_FILE:-}"
CLEANUP_EXISTING="${CLEANUP_EXISTING:-1}"
PODMAN_SOCKET_DEMO_OWNED="${PODMAN_SOCKET_DEMO_OWNED:-0}"
PODMAN_SERVICE_PID="${PODMAN_SERVICE_PID:-}"

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

cleanup_container() {
    podman rm -f "$1" >/dev/null 2>&1 || true
}

ensure_network() {
    if ! podman network exists "$PODMAN_NETWORK" >/dev/null 2>&1; then
        run podman network create "$PODMAN_NETWORK"
    fi
}

normalize_podman_socket_path() {
    local socket_path="$1"
    if [[ "$socket_path" == unix://* ]]; then
        socket_path="${socket_path#unix://}"
    fi
    printf "%s\n" "$socket_path"
}

detect_podman_socket() {
    if [[ -n "${PODMAN_SOCKET:-}" ]]; then
        normalize_podman_socket_path "$PODMAN_SOCKET"
        return
    fi
    if [[ -n "${XDG_RUNTIME_DIR:-}" && -S "${XDG_RUNTIME_DIR}/podman/podman.sock" ]]; then
        printf "%s\n" "${XDG_RUNTIME_DIR}/podman/podman.sock"
        return
    fi
    if [[ -S "/run/user/$(id -u)/podman/podman.sock" ]]; then
        printf "%s\n" "/run/user/$(id -u)/podman/podman.sock"
        return
    fi
    if [[ -S /run/podman/podman.sock ]]; then
        printf "%s\n" /run/podman/podman.sock
        return
    fi
    if [[ -S /var/run/docker.sock ]]; then
        printf "%s\n" /var/run/docker.sock
        return
    fi
    local connection_socket
    connection_socket="$(
        podman system connection list --format '{{.URI}}' 2>/dev/null |
            sed -n 's|^unix://||p' |
            while IFS= read -r candidate; do
                if [[ -S "$candidate" ]]; then
                    printf "%s\n" "$candidate"
                    break
                fi
            done
    )"
    if [[ -n "$connection_socket" ]]; then
        printf "%s\n" "$connection_socket"
        return
    fi
    return 1
}

start_podman_service() {
    local socket_path="$1"
    local log_path="${SPIRE_STATE_DIR}/podman/podman-service.log"

    mkdir -p "$(dirname "$socket_path")"
    printf "\n$ podman system service --time=0 unix://%s\n" "$socket_path" >&2
    podman system service --time=0 "unix://${socket_path}" >"$log_path" 2>&1 &
    PODMAN_SERVICE_PID="$!"

    for _ in $(seq 1 80); do
        if [[ -S "$socket_path" ]]; then
            chmod 0666 "$socket_path" || true
            return 0
        fi
        if ! kill -0 "$PODMAN_SERVICE_PID" >/dev/null 2>&1; then
            wait "$PODMAN_SERVICE_PID" || true
            PODMAN_SERVICE_PID=""
            break
        fi
        sleep 0.25
    done

    printf "Podman API service did not create %s; set PODMAN_SOCKET to a running API socket\n" "$socket_path" >&2
    if [[ -s "$log_path" ]]; then
        cat "$log_path" >&2
    fi
    return 1
}

ensure_podman_socket() {
    local detected_socket
    if detected_socket="$(detect_podman_socket)"; then
        PODMAN_SOCKET="$detected_socket"
        PODMAN_SOCKET_DEMO_OWNED="0"
        return 0
    fi

    local socket_path="${SPIRE_STATE_DIR}/podman/podman.sock"
    printf "\nNo Podman API socket found; starting a temporary rootless Podman API service.\n" >&2
    start_podman_service "$socket_path"
    PODMAN_SOCKET="$socket_path"
    PODMAN_SOCKET_DEMO_OWNED="1"
}

podman_socket_volume() {
    local source="$1"
    local target="$2"
    if [[ "$PODMAN_SOCKET_DEMO_OWNED" == "1" ]]; then
        printf "%s:%s:z\n" "$source" "$target"
    else
        printf "%s:%s\n" "$source" "$target"
    fi
}

quote_env_value() {
    printf "%q" "$1"
}

write_env_line() {
    local name="$1"
    local value="$2"
    if [[ -n "$SPIRE_ENV_FILE" ]]; then
        printf "%s=%s\n" "$name" "$(quote_env_value "$value")" >>"$SPIRE_ENV_FILE"
    fi
}

reset_env_file() {
    if [[ -n "$SPIRE_ENV_FILE" ]]; then
        mkdir -p "$(dirname "$SPIRE_ENV_FILE")"
        : >"$SPIRE_ENV_FILE"
    fi
}

copy_config() {
    local source="$1"
    local dest="$2"
    mkdir -p "$(dirname "$dest")"
    cp "$source" "$dest"
    chmod 0644 "$dest"
}

wait_for_socket() {
    local path="$1"
    local label="$2"
    for _ in $(seq 1 80); do
        if [[ -S "$path" ]]; then
            return
        fi
        sleep 0.25
    done
    printf "%s was not created at %s\n" "$label" "$path" >&2
    return 1
}

wait_for_http() {
    local url="$1"
    local label="$2"
    for _ in $(seq 1 80); do
        if curl -fsS "$url" >/dev/null 2>&1; then
            return
        fi
        sleep 0.25
    done
    printf "%s did not become ready at %s\n" "$label" "$url" >&2
    return 1
}
