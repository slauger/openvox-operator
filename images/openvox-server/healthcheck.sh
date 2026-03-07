#!/usr/bin/env bash
# Simple healthcheck for Docker/Podman.
# K8s uses livenessProbe/readinessProbe/startupProbe instead.

set -e

timeout="${1:-10}"

curl --fail \
    --silent \
    --max-time "${timeout}" \
    --insecure \
    "https://localhost:8140/status/v1/simple" \
    | grep -q '^running$' \
    || exit 1
