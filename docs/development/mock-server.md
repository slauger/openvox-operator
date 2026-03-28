# openvox-mock

`openvox-mock` is a lightweight mock server for E2E testing. It provides ENC, report, and OpenVox DB endpoints in a single binary with no external dependencies.

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/node/{certname}` | ENC classification (returns Puppet ENC YAML) |
| `POST` | `/reports` | Receive Puppet reports |
| `POST` | `/pdb/cmd/v1` | Receive PuppetDB Wire Format commands |
| `GET` | `/api/reports` | List all received reports (JSON) |
| `GET` | `/api/pdb-commands` | List all received PDB commands (JSON) |
| `GET` | `/api/classifications` | List all served classifications (JSON) |
| `GET` | `/healthz` | Health check |

The `/api/*` endpoints are useful for assertions in E2E tests. They return all data the mock has received or served during its lifetime.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN` | `:8080` | Listen address |
| `ENC_CLASSES` | - | Comma-separated list of Puppet classes to return for all nodes |
| `ENC_ENVIRONMENT` | - | Puppet environment to return for all nodes |
| `CLASSIFICATIONS_FILE` | - | Path to a YAML file with per-node classifications |
| `AUTH_TYPE` | - | Authentication method: `bearer`, `basic`, or `token` |
| `AUTH_TOKEN` | - | Token value (for `bearer` and `token` auth) |
| `AUTH_HEADER` | `X-Auth-Token` | Custom header name (for `token` auth) |
| `AUTH_USERNAME` | - | Username (for `basic` auth) |
| `AUTH_PASSWORD` | - | Password (for `basic` auth) |

## Classification

### Static (Environment Variables)

Set `ENC_CLASSES` and `ENC_ENVIRONMENT` to return the same classification for all nodes:

```bash
ENC_CLASSES="role::webserver,profile::base" \
ENC_ENVIRONMENT="production" \
openvox-mock
```

Every `GET /node/{certname}` request returns:

```yaml
---
environment: production
classes:
  role::webserver:
  profile::base:
```

### File-Based (Per-Node)

Set `CLASSIFICATIONS_FILE` to a YAML file for per-node classifications:

```yaml
# classifications.yaml
webserver01.example.com:
  classes:
    - role::webserver
    - profile::base
  environment: production
dbserver01.example.com:
  classes:
    - role::database
  environment: staging
_default:
  classes:
    - profile::base
  environment: production
```

The `_default` key is used as a fallback when a certname is not found. If neither file-based nor env-var classification matches, an empty response is returned.

The classifications file is automatically reloaded every 5 seconds when modified (hot-reload).

## Authentication

When `AUTH_TYPE` is set, all ENC, report, and PDB endpoints require authentication. The `/api/*` and `/healthz` endpoints are always unauthenticated.

=== "Bearer Token"

    ```bash
    AUTH_TYPE=bearer AUTH_TOKEN=my-secret openvox-mock
    ```

    Expects: `Authorization: Bearer my-secret`

=== "Basic Auth"

    ```bash
    AUTH_TYPE=basic AUTH_USERNAME=admin AUTH_PASSWORD=secret openvox-mock
    ```

    Expects: standard HTTP Basic Authentication

=== "Custom Token Header"

    ```bash
    AUTH_TYPE=token AUTH_TOKEN=my-secret AUTH_HEADER=X-Api-Key openvox-mock
    ```

    Expects: `X-Api-Key: my-secret`

## PuppetDB Command Validation

The `/pdb/cmd/v1` endpoint validates the PuppetDB Wire Format envelope:

- `command` field must be present
- `version` field must be non-zero
- `store report` commands must use version 8
