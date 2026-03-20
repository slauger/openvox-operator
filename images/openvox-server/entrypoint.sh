#!/bin/bash
# Rootless entrypoint for OpenVox Server.
# Starts puppetserver directly via java -- no ezbake, no runuser, no sudo.

set -o errexit
set -o pipefail
set -o nounset

# Inline defaults (previously from /etc/default/puppetserver)
JAVA_BIN="/usr/bin/java"
JAVA_ARGS="${JAVA_ARGS:--Xms1024m -Xmx1024m}"
INSTALL_DIR="/opt/puppetlabs/server/apps/puppetserver"
CONFIG="/etc/puppetlabs/puppetserver/conf.d"
BOOTSTRAP_CONFIG="/etc/puppetlabs/puppetserver/services.d/,/opt/puppetlabs/server/apps/puppetserver/config/services.d/"

echo "Starting OpenVox Server (direct java, PID $$)"

# Start puppetserver directly -- the core from ezbake's foreground script,
# without the user-switching and PID file overhead.
# shellcheck disable=SC2086 # JAVA_ARGS word splitting is intentional
exec "${JAVA_BIN}" ${JAVA_ARGS} \
    --add-opens java.base/sun.nio.ch=ALL-UNNAMED \
    --add-opens java.base/java.io=ALL-UNNAMED \
    -Dlogappender=STDOUT \
    -cp "${INSTALL_DIR}/jvm-ssl-utils-patch.jar:${INSTALL_DIR}/puppet-server-release.jar" \
    clojure.main -m puppetlabs.trapperkeeper.main \
    --config "${CONFIG}" \
    --bootstrap-config "${BOOTSTRAP_CONFIG}"
