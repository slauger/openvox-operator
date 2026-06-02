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

### CA Expiring

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
