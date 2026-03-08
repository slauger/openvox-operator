package controller

// buildServerCertScript returns the shell script for SSL bootstrap against a running CA.
func buildServerCertScript() string {
	return `#!/bin/bash
set -euo pipefail

echo "Waiting for CA server at ${CA_SERVICE}..."
until curl --fail --silent --insecure "https://${CA_SERVICE}:8140/status/v1/simple" | grep -q running; do
  sleep 2
done

echo "Bootstrapping SSL..."
ARGS="--server=${CA_SERVICE} --serverport=8140 --certname=${CERTNAME}"
if [ -n "${DNS_ALT_NAMES}" ]; then
  ARGS="${ARGS} --dns_alt_names=${DNS_ALT_NAMES}"
fi
puppet ssl bootstrap ${ARGS}
echo "SSL bootstrap complete."

NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)
TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
API="https://kubernetes.default.svc/api/v1/namespaces/${NAMESPACE}/secrets"

CERT=$(base64 -w0 /etc/puppetlabs/puppet/ssl/certs/${CERTNAME}.pem)
KEY=$(base64 -w0 /etc/puppetlabs/puppet/ssl/private_keys/${CERTNAME}.pem)

PAYLOAD="{
  \"apiVersion\": \"v1\",
  \"kind\": \"Secret\",
  \"metadata\": {
    \"name\": \"${SSL_SECRET_NAME}\",
    \"namespace\": \"${NAMESPACE}\",
    \"labels\": {
      \"app.kubernetes.io/managed-by\": \"openvox-operator\",
      \"app.kubernetes.io/name\": \"openvox\",
      \"openvox.voxpupuli.org/environment\": \"${ENV_NAME}\",
      \"openvox.voxpupuli.org/certificate\": \"${SERVER_NAME}\"
    }
  },
  \"data\": {
    \"cert.pem\": \"${CERT}\",
    \"key.pem\": \"${KEY}\"
  }
}"

HTTP_CODE=$(curl -sk -o /tmp/api-response -w '%{http_code}' -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  "${API}/${SSL_SECRET_NAME}" -d "$PAYLOAD")

if [ "$HTTP_CODE" = "404" ]; then
  HTTP_CODE=$(curl -sk -o /tmp/api-response -w '%{http_code}' -X POST \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    "${API}" -d "$PAYLOAD")
fi

if [ "${HTTP_CODE:0:1}" != "2" ]; then
  echo "Failed to create/update SSL Secret (HTTP ${HTTP_CODE}):" >&2
  cat /tmp/api-response >&2
  exit 1
fi

echo "SSL Secret '${SSL_SECRET_NAME}' created successfully."
`
}
