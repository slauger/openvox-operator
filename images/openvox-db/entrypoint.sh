#!/bin/bash
# Rootless entrypoint for OpenVox DB.
# Starts OpenVox DB directly via java -- no ezbake, no runuser, no sudo.

set -o errexit
set -o pipefail
set -o nounset

JAVA_BIN="/usr/bin/java"
JAVA_ARGS="${JAVA_ARGS:--Xms256m -Xmx256m} -Dlogappender=STDOUT"
INSTALL_DIR="/opt/puppetlabs/server/apps/puppetdb"
CONFIG="/etc/puppetlabs/puppetdb/conf.d"
BOOTSTRAP_CONFIG="/etc/puppetlabs/puppetdb/bootstrap.cfg"

echo "Starting OpenVox DB (direct java, PID $$)"

# shellcheck disable=SC2086 # JAVA_ARGS word splitting is intentional
exec "${JAVA_BIN}" ${JAVA_ARGS} \
    -cp "${INSTALL_DIR}/puppetdb.jar" \
    clojure.main -m puppetlabs.puppetdb.cli.services \
    --config "${CONFIG}" \
    --bootstrap-config "${BOOTSTRAP_CONFIG}"
