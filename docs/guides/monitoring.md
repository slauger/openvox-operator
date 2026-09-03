# Monitoring

The operator exposes Prometheus metrics for observability. This guide covers the available metrics, how to enable scraping, and example alert rules.

## Metrics Endpoint

The operator serves metrics on port `8080` at `/metrics` by default. This includes both [controller-runtime default metrics](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/metrics) and custom operator metrics.

### Disabling Metrics

To disable the metrics endpoint entirely, set `metrics.enabled: false` in the Helm values:

```yaml
metrics:
  enabled: false
```

This passes `--metrics-bind-address=0` to the operator and removes the metrics port from the Deployment.

## Custom Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `openvox_server_replicas_desired` | Gauge | `name`, `namespace` | Desired number of replicas for a Server CR |
| `openvox_server_replicas_ready` | Gauge | `name`, `namespace` | Number of ready replicas for a Server CR |
| `openvox_certificate_expiry_timestamp_seconds` | Gauge | `name`, `namespace` | Unix timestamp when a certificate or CA expires |
| `openvox_crl_last_refresh_timestamp_seconds` | Gauge | `name`, `namespace` | Unix timestamp of the last successful CRL refresh for a CertificateAuthority |

All series are removed when the resource they describe is deleted, so alerts
do not keep firing for objects that no longer exist.

### Controller-Runtime Metrics

These are automatically available:

| Metric | Description |
|--------|-------------|
| `controller_runtime_reconcile_total` | Total reconciliations per controller |
| `controller_runtime_reconcile_errors_total` | Reconciliation errors per controller |
| `controller_runtime_reconcile_time_seconds` | Reconciliation duration histogram |
| `workqueue_depth` | Current work queue depth |

## Prometheus Integration

### Metrics Service

A ClusterIP Service is created by default to expose the metrics endpoint:

```yaml
metrics:
  enabled: true
  port: 8080
  service:
    enabled: true
```

### ServiceMonitor

For clusters running the [Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator), enable the ServiceMonitor:

```yaml
metrics:
  serviceMonitor:
    enabled: true
    interval: 30s
    labels: {}  # additional labels for ServiceMonitor selection
```

The ServiceMonitor selects the metrics Service by the standard `app.kubernetes.io/name` and `app.kubernetes.io/instance` labels.

### Pod Annotations

For Prometheus setups that use annotation-based discovery instead of ServiceMonitor:

```yaml
podAnnotations:
  prometheus.io/scrape: "true"
  prometheus.io/port: "8080"
  prometheus.io/path: "/metrics"
```

## Example Alert Rules

### Server Degraded

Alert when a Server CR has fewer ready replicas than desired for more than 5 minutes:

```yaml
groups:
  - name: openvox
    rules:
      - alert: OpenVoxServerDegraded
        expr: openvox_server_replicas_ready < openvox_server_replicas_desired
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "OpenVox Server {{ $labels.name }} is degraded"
          description: "{{ $labels.name }} in {{ $labels.namespace }} has {{ $value }} ready replicas (desired: {{ with printf `openvox_server_replicas_desired{name=\"%s\",namespace=\"%s\"}` $labels.name $labels.namespace | query }}{{ . | first | value }}{{ end }})"
```

### Certificate Expiring

Alert when a certificate expires within 30 days:

```yaml
      - alert: OpenVoxCertExpiringSoon
        expr: openvox_certificate_expiry_timestamp_seconds - time() < 30 * 24 * 3600
        for: 1h
        labels:
          severity: warning
        annotations:
          summary: "OpenVox certificate {{ $labels.name }} expires soon"
          description: "{{ $labels.name }} in {{ $labels.namespace }} expires in {{ $value | humanizeDuration }}"
```

### Missing CRL series

A stale CRL is alertable only while the series exists. If the operator never
refreshed the CRL - it crashed early, or the CA never became ready - there is
no series at all and the staleness rule below stays silent. Alert on the
absence separately:

```yaml
      - alert: OpenVoxCRLMetricMissing
        expr: absent(openvox_crl_last_refresh_timestamp_seconds)
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "No CRL refresh has been recorded"
          description: "The operator has not refreshed any CRL since it started. Revocations are not reaching agents."
```

### Stale CRL

A CRL that is no longer refreshed means revoked agents keep being accepted.
Alert when the last successful refresh is more than a day old:

```yaml
      - alert: OpenVoxCRLStale
        expr: time() - openvox_crl_last_refresh_timestamp_seconds > 24 * 3600
        for: 1h
        labels:
          severity: critical
        annotations:
          summary: "OpenVox CRL for {{ $labels.name }} is stale"
          description: "The CRL of {{ $labels.name }} in {{ $labels.namespace }} was last refreshed {{ $value | humanizeDuration }} ago, so revoked agents may still be accepted"
```

### CA Expiring

!!! note "CAs and certificates share one metric"

    `openvox_certificate_expiry_timestamp_seconds` is written by both the
    Certificate and the CertificateAuthority controller, with the same
    `name`/`namespace` labels and nothing that says which kind a series belongs
    to. Telling them apart in a query means matching on the name.

    The rule below uses `.*-ca`, which is a convention rather than a guarantee:
    it also catches a Certificate that happens to end in `-ca`, and it misses a
    CertificateAuthority named otherwise. Replace the matcher with your actual
    CA names if you rely on the distinction.

Alert when a CA certificate expires within 90 days:

```yaml
      - alert: OpenVoxCAExpiringSoon
        expr: openvox_certificate_expiry_timestamp_seconds{name=~".*-ca"} - time() < 90 * 24 * 3600
        for: 1h
        labels:
          severity: critical
        annotations:
          summary: "OpenVox CA {{ $labels.name }} expires soon"
          description: "CA {{ $labels.name }} in {{ $labels.namespace }} expires in {{ $value | humanizeDuration }}"
```
