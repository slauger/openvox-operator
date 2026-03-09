# Traffic Flow

Each Pool owns a Kubernetes Service that selects Server pods by label. The CA server can participate in both pools - handling CA requests via its dedicated pool and also serving catalog requests through the server pool.

## LoadBalancer Services

The simplest setup - each Pool gets its own external IP:

```mermaid
graph LR
    Agent["Agents"] --> LB
    Agent --> CA_SVC

    subgraph Kubernetes
        LB["Pool: puppet<br/>Service (LoadBalancer)"]
        CA_SVC["Pool: puppet-ca<br/>Service (LoadBalancer)"]

        LB --> CA["Server: ca<br/>replicas: 1"]
        LB --> Stable["Server: stable<br/>replicas: 3"]
        LB --> Canary["Server: canary<br/>replicas: 1"]

        CA_SVC --> CA
    end
```

This works well for single-environment setups. For multiple environments, each Pool creates a separate LoadBalancer, which can become expensive.

## Gateway API TLSRoute

All Pools share a single LoadBalancer, routed by SNI hostname. Since Puppet uses mTLS, TLS passthrough is required - the Gateway does not terminate TLS.

```mermaid
graph LR
    Agent["Agents"] --> GW

    subgraph Kubernetes
        GW["Gateway<br/>(shared LoadBalancer)"]

        GW --> TR1["TLSRoute<br/>puppet.example.com"]
        GW --> TR2["TLSRoute<br/>puppet-ca.example.com"]
        TR1 --> LB["Pool: puppet<br/>Service (ClusterIP)"]
        TR2 --> CA_SVC["Pool: puppet-ca<br/>Service (ClusterIP)"]

        LB --> CA["Server: ca<br/>replicas: 1"]
        LB --> Stable["Server: stable<br/>replicas: 3"]
        LB --> Canary["Server: canary<br/>replicas: 1"]

        CA_SVC --> CA
    end
```

With this setup, Pools use ClusterIP Services and the Gateway handles external access. Adding a new environment only requires a new TLSRoute - no additional LoadBalancer.

See [Gateway API](gateway-api.md) for the full configuration guide.
