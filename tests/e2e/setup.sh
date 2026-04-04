#!/usr/bin/env bash
set -euo pipefail

CNPG_VERSION="${CNPG_VERSION:-1.26.2}"
ENVOY_GATEWAY_VERSION="${ENVOY_GATEWAY_VERSION:-v1.7.1}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.17.2}"

usage() {
  cat <<EOF
Usage: $0 <command>

Commands:
  install-cnpg            Install CloudNativePG operator
  install-envoy-gateway   Install Envoy Gateway (incl. Gateway API CRDs)
  install-cert-manager    Install cert-manager
  all                     Install all dependencies
  status                  Show status of all dependencies
EOF
}

install_cnpg() {
  echo "Installing CloudNativePG v${CNPG_VERSION}..."
  kubectl apply --server-side \
    -f "https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v${CNPG_VERSION}/cnpg-${CNPG_VERSION}.yaml"
  kubectl wait --for=condition=Available \
    deployment/cnpg-controller-manager -n cnpg-system --timeout=3m
  echo "CloudNativePG is ready."
}

install_envoy_gateway() {
  echo "Installing Envoy Gateway ${ENVOY_GATEWAY_VERSION}..."
  kubectl apply --server-side \
    -f "https://github.com/envoyproxy/gateway/releases/download/${ENVOY_GATEWAY_VERSION}/install.yaml"
  kubectl wait --for=condition=Available \
    deployment/envoy-gateway -n envoy-gateway-system --timeout=3m
  echo "Envoy Gateway is ready."
}

install_cert_manager() {
  echo "Installing cert-manager ${CERT_MANAGER_VERSION}..."
  kubectl apply --server-side \
    -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
  kubectl wait --for=condition=Available \
    deployment/cert-manager -n cert-manager --timeout=3m
  kubectl wait --for=condition=Available \
    deployment/cert-manager-webhook -n cert-manager --timeout=3m
  echo "cert-manager is ready."
}

check_deployment() {
  local ns="$1" name="$2"
  if kubectl get deployment "$name" -n "$ns" &>/dev/null; then
    local avail
    avail=$(kubectl get deployment "$name" -n "$ns" \
      -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')
    if [ "$avail" = "True" ]; then
      echo "  $name ($ns): Ready"
    else
      echo "  $name ($ns): Not Ready"
    fi
  else
    echo "  $name ($ns): Not Installed"
  fi
}

status() {
  echo "E2E Dependency Status:"
  check_deployment cnpg-system cnpg-controller-manager
  check_deployment envoy-gateway-system envoy-gateway
  check_deployment cert-manager cert-manager
  check_deployment cert-manager cert-manager-webhook
}

case "${1:-}" in
  install-cnpg)         install_cnpg ;;
  install-envoy-gateway) install_envoy_gateway ;;
  install-cert-manager) install_cert_manager ;;
  all)
    install_cnpg
    install_envoy_gateway
    install_cert_manager
    ;;
  status) status ;;
  *)
    usage
    exit 1
    ;;
esac
