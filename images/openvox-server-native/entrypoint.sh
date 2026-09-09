#!/bin/bash
# Rootless entrypoint for the experimental native (no-JVM) OpenVox Server.
#
# Layout:  Go edge (mTLS + auth) on :8140  -->  Puma/CRuby compile backend on :9140
#
# TLS certs: if the operator mounted a Puppet-signed server certificate under the
# standard ssldir (the same Secret the JVM server consumes), the edge uses it.
# Otherwise a throwaway self-signed PoC PKI is generated so the image also runs
# standalone (podman run) without the operator/CA.

set -o errexit
set -o pipefail
set -o nounset

export PATH="/opt/puppetlabs/bin:/opt/puppetlabs/puppet/bin:${PATH}"
export GEM_HOME="${GEM_HOME:-/opt/openvox-native}"
export GEM_PATH="${GEM_HOME}"
export CODEDIR="${CODEDIR:-/etc/puppetlabs/code}"

SSLDIR="${SSLDIR:-/etc/puppetlabs/puppet/ssl}"
CERTNAME="${CERTNAME:-$(hostname -f 2>/dev/null || hostname)}"
EDGE_DIR="/tmp/edge"

mkdir -p "${EDGE_DIR}" /var/run/puppetlabs /var/log/puppetlabs \
         "${GEM_HOME}" "/opt/puppetlabs/server/data/puppetserver"

server_cert="${SSLDIR}/certs/${CERTNAME}.pem"
server_key="${SSLDIR}/private_keys/${CERTNAME}.pem"
ca_cert="${SSLDIR}/certs/ca.pem"

if [[ -f "${server_cert}" && -f "${server_key}" && -f "${ca_cert}" ]]; then
    echo "Using operator-provided certificate for ${CERTNAME} from ${SSLDIR}"
    export EDGE_TLS_CERT="${server_cert}"
    export EDGE_TLS_KEY="${server_key}"
    export EDGE_TLS_CA="${ca_cert}"
else
    echo "No operator certificate found for ${CERTNAME}; generating a self-signed PoC PKI in ${EDGE_DIR}"
    if [[ ! -f "${EDGE_DIR}/ca.pem" ]]; then
        openssl req -x509 -newkey rsa:2048 -nodes \
            -keyout "${EDGE_DIR}/ca-key.pem" -out "${EDGE_DIR}/ca.pem" \
            -days 3650 -subj "/CN=openvox-native-poc-ca" 2>/dev/null
        openssl req -newkey rsa:2048 -nodes \
            -keyout "${EDGE_DIR}/server-key.pem" -out "${EDGE_DIR}/server.csr" \
            -subj "/CN=${CERTNAME}" 2>/dev/null
        openssl x509 -req -in "${EDGE_DIR}/server.csr" \
            -CA "${EDGE_DIR}/ca.pem" -CAkey "${EDGE_DIR}/ca-key.pem" -CAcreateserial \
            -out "${EDGE_DIR}/server.pem" -days 825 \
            -extfile <(printf "subjectAltName=DNS:%s,DNS:localhost,IP:127.0.0.1" "${CERTNAME}") 2>/dev/null
        # A demo agent cert (CN=agent.example) for manual smoke tests against :8140.
        openssl req -newkey rsa:2048 -nodes \
            -keyout "${EDGE_DIR}/agent-key.pem" -out "${EDGE_DIR}/agent.csr" \
            -subj "/CN=agent.example" 2>/dev/null
        openssl x509 -req -in "${EDGE_DIR}/agent.csr" \
            -CA "${EDGE_DIR}/ca.pem" -CAkey "${EDGE_DIR}/ca-key.pem" -CAcreateserial \
            -out "${EDGE_DIR}/agent.pem" -days 825 2>/dev/null
        echo "PoC PKI generated (CA, server cert for ${CERTNAME}, demo agent cert CN=agent.example)"
    fi
    export EDGE_TLS_CERT="${EDGE_DIR}/server.pem"
    export EDGE_TLS_KEY="${EDGE_DIR}/server-key.pem"
    export EDGE_TLS_CA="${EDGE_DIR}/ca.pem"
fi

export EDGE_AUTH_RULES="${EDGE_AUTH_RULES:-/etc/edge/auth-rules.json}"
export EDGE_BACKEND_URL="${EDGE_BACKEND_URL:-http://127.0.0.1:9140}"

# Start the CRuby compile backend (localhost only, behind the edge).
cd /opt/openvox-native/backend
puma -C puma.rb config.ru &
backend_pid=$!

echo "Waiting for the compile backend on 127.0.0.1:9140 ..."
for _ in $(seq 1 60); do
    if ruby -rsocket -e 'TCPSocket.new("127.0.0.1", 9140).close' 2>/dev/null; then
        break
    fi
    if ! kill -0 "${backend_pid}" 2>/dev/null; then
        echo "ERROR: compile backend exited during startup" >&2
        exit 1
    fi
    sleep 1
done
echo "Compile backend is up (pid ${backend_pid})"

# Reap the backend if the edge exits, and vice-versa.
trap 'kill "${backend_pid}" 2>/dev/null || true' EXIT

exec /usr/local/bin/openvox-edge
