#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

TRUST_DOMAIN="${TRUST_DOMAIN:-openshell.local}"
KEYCLOAK_CONTAINER="${KEYCLOAK_CONTAINER:-perilinkle-keycloak}"
KEYCLOAK_SPIFFE_ID="${KEYCLOAK_SPIFFE_ID:-spiffe://${TRUST_DOMAIN}/podman/keycloak/${KEYCLOAK_CONTAINER}}"
SPIRE_AGENT_PARENT_ID="${SPIRE_AGENT_PARENT_ID:-spiffe://${TRUST_DOMAIN}/openshell/spire-agent/perilinkle}"
SPIRE_SERVER_SOCKET="${SPIRE_SERVER_SOCKET:-/run/spire/server/private/api.sock}"
SPIRE_SERVER_CONTAINER="${SPIRE_SERVER_CONTAINER:-perilinkle-spire-server}"
SPIRE_SERVER_BIN="${SPIRE_SERVER_BIN:-/opt/spire/bin/spire-server}"

if [[ -n "${KEYCLOAK_SELECTORS:-}" ]]; then
    read -r -a selectors <<<"$KEYCLOAK_SELECTORS"
else
    selectors=("docker:label:app:keycloak")
fi

args=(
    entry create
    -socketPath "$SPIRE_SERVER_SOCKET"
    -parentID "$SPIRE_AGENT_PARENT_ID"
    -spiffeID "$KEYCLOAK_SPIFFE_ID"
)

for selector in "${selectors[@]}"; do
    args+=(-selector "$selector")
done

printf "Registering Keycloak SPIFFE entry: %s\n" "$KEYCLOAK_SPIFFE_ID" >&2
podman exec "$SPIRE_SERVER_CONTAINER" "$SPIRE_SERVER_BIN" "${args[@]}"
