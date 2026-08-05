# Bundled demo environment so the image compiles a catalog out of the box.
# Replace it via a code volume (config.code) exactly like the JVM server image.
node default {
  notify { 'openvox-server-native':
    message => 'Catalog compiled by native CRuby behind the Go edge (no JVM)',
  }
}
