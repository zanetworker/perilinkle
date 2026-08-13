#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

TRUST_DOMAIN="${TRUST_DOMAIN:-openshell.local}"
GATEWAY_ID="${GATEWAY_ID:-perilinkle-podman}"
GATEWAY_SPIFFE_ID="${GATEWAY_SPIFFE_ID:-spiffe://${TRUST_DOMAIN}/podman/gateway/${GATEWAY_ID}}"
SPIRE_AGENT_PARENT_ID="${SPIRE_AGENT_PARENT_ID:-spiffe://${TRUST_DOMAIN}/openshell/spire-agent/perilinkle}"
SPIRE_SERVER_SOCKET="${SPIRE_SERVER_SOCKET:-/run/spire/server/private/api.sock}"
SPIRE_SERVER_CONTAINER="${SPIRE_SERVER_CONTAINER:-perilinkle-spire-server}"
SPIRE_SERVER_BIN="${SPIRE_SERVER_BIN:-/opt/spire/bin/spire-server}"

if [[ -n "${GATEWAY_SELECTORS:-}" ]]; then
    read -r -a selectors <<<"$GATEWAY_SELECTORS"
else
    selectors=(
        "docker:label:openshell.managed:true"
        "docker:label:openshell.ai/gateway-id:${GATEWAY_ID}"
    )
fi

args=(
    entry create
    -socketPath "$SPIRE_SERVER_SOCKET"
    -parentID "$SPIRE_AGENT_PARENT_ID"
    -spiffeID "$GATEWAY_SPIFFE_ID"
    -jwtSVIDTTL 300
)

for selector in "${selectors[@]}"; do
    args+=(-selector "$selector")
done

printf "Registering gateway SPIFFE entry: %s\n" "$GATEWAY_SPIFFE_ID" >&2
podman exec "$SPIRE_SERVER_CONTAINER" "$SPIRE_SERVER_BIN" "${args[@]}"
