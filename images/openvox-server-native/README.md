# openvox-server-native (experimental)

A non-JVM OpenVox Server image. It keeps the same rootless/OpenShift conventions
and `:8140` mTLS surface as [`images/openvox-server`](../openvox-server), but
replaces the JRuby pool and the JVM with:

```
          :8140 (mTLS)                     127.0.0.1:9140
  agent ───────────────▶  Go edge  ──────────────────────▶  Puma (cluster) ─▶ CRuby + openvox gem
                          mTLS + auth.conf rules            preload_app!       catalog compilation
```

- **Go edge** (`cmd/edge`) terminates mTLS and does authorization — the job
  trapperkeeper-authorization does on the JVM server. Rules are **config-driven**
  (`auth-rules.json`), modeled on the operator's `builtinAuthRules`, so the
  operator could render this file from the same authorization intent it already
  renders as HOCON `auth.conf`.
- **CRuby backend** (`backend/config.ru`) drives the very same Puppet indirections
  as openvox-server's `compiler.rb` (`node = plain`, `catalog = compiler`) — only
  on plain MRI instead of JRuby.
- **Puma** runs one worker per core, one thread each (catalog compile is CPU-bound
  and CRuby holds the GIL), with `preload_app!` for copy-on-write sharing of the
  loaded environment. Scale out with pod replicas + an HPA.

## Scope

This is the **server role only** (catalog compilation). The **CA role stays on the
JVM `openvox-server` image** — the native image is meant to be swapped in for
server-only pods while the CA is left as-is. No RPM install of OpenVox: the Ruby
interpreter comes from the ubi `ruby` module (interpreter only), OpenVox itself is
the `openvox` gem in a self-contained `GEM_HOME` (the CRuby analogue of the
server tarball).

## auth-rules.json

Config-driven equivalent of `auth.conf`. Each rule mirrors a `match-request`
block; the first rule whose `path` + `method` matches decides, `deny` before
`allow`, and unmatched requests fall through to `default` (`deny`).

| Field | Meaning |
|-------|---------|
| `path` + `type` | `regex` (matched with captures) or `path` (prefix) |
| `method` | list of HTTP methods; empty = any |
| `allow` | `"*"` (any authenticated), `"$1"` (client CN must equal capture group 1), a literal certname, `{"extensions": {"pp_cli_auth": "true"}}`, or a list mixing these |
| `deny` | same forms as `allow`; takes precedence |
| `allow_unauthenticated` | serve without a client certificate (e.g. `/status/`) |

The `"$1"` matcher is the dynamic catalog rule (`allow "$1"`) plain reverse
proxies can't express: a client may only fetch the catalog for its own certname.
Certificate extensions (`pp_cli_auth`, `pp_authorization`, `pp_preshared_key`, …)
are read straight from the client cert.

## Build

From the repo root:

```sh
podman build -t openvox-server-native:latest -f images/openvox-server-native/Containerfile .
```

## Run standalone

With no operator-provided certificate, the entrypoint generates a throwaway
self-signed PoC PKI (CA, server cert, and a demo agent cert `CN=agent.example`)
under `/tmp/edge`:

```sh
podman run --rm --name ovnative -p 8140:8140 openvox-server-native:latest

# health (unauthenticated)
curl -sk https://localhost:8140/status/v1/simple      # -> running

# compile a catalog as the demo agent (auth.conf allow "$1": CN must equal the node)
podman exec ovnative sh -c '
  curl -sk https://localhost:8140/puppet/v3/catalog/agent.example \
    --cert /tmp/edge/agent.pem --key /tmp/edge/agent-key.pem \
    -H "Content-Type: application/json" -d "{\"values\":{}}"'

# a different node is refused (403) — the dynamic allow "$1" rule
podman exec ovnative sh -c '
  curl -sk https://localhost:8140/puppet/v3/catalog/other.example \
    --cert /tmp/edge/agent.pem --key /tmp/edge/agent-key.pem -d "{}" -w "\n%{http_code}\n"'
```

## Run under the operator

When the operator mounts a Puppet-signed server certificate under the standard
`ssldir` (`/etc/puppetlabs/puppet/ssl`, `CERTNAME` = the server certname) — the
same Secret the JVM server consumes — the edge uses it instead of the PoC PKI.
Point a server-role pod's image at this build and mount the control repo via
`config.code`, exactly like the JVM server.

## Status

Experimental / proof-of-concept. Known gaps vs. the JVM server: no CA endpoints
(delegated to the JVM CA), a subset of the v3 API (catalog/node/file/report/
environments/status), no PuppetDB termini wiring, and CRuby's copy-on-write
sharing erodes under GC at scale — the JRuby pool's warm in-process caches remain
the stronger option for very large fleets. See the top-level design notes for the
full trade-off analysis.
