<p align="center">
  <img src="logo.svg" alt="MINK - Musil IN Kubernetes" width="600"/>
</p>

# MINK

A Kubernetes operator for deploying and managing [Musil](https://github.com/jmpargana/musil) message brokers and topics.

## Overview

MINK automates the lifecycle of Musil brokers in Kubernetes. It provides two Custom Resource Definitions:

- **Broker** — deploys a Musil broker as a StatefulSet with a headless Service, persistent storage, and per-node configuration
- **Topic** — seeds topics into a running broker via a one-shot Job using the Musil seeder

## Quick Start

### Prerequisites

- Kubernetes cluster (v1.28+)
- `kubectl` configured
- CRDs installed (`make install`)

### Deploy a Broker

```yaml
apiVersion: mink.io.musil/v1
kind: Broker
metadata:
  name: my-broker
spec:
  storageSize: "1Gi"
  replicas: 1
  port: 9092
```

### Create a Topic

```yaml
apiVersion: mink.io.musil/v1
kind: Topic
metadata:
  name: events
spec:
  name: "events"
  numPartitions: 3
  replicationFactor: 1
  brokerRef: "my-broker"
```

## CRD Reference

### Broker Spec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `image` | string | `ghcr.io/jmpargana/musil-server:0.1.5` | Broker container image |
| `replicas` | int32 | 1 | Number of broker replicas |
| `storageSize` | string | *required* | PVC size (e.g. `"1Gi"`) |
| `storageClassName` | string | cluster default | Storage class for PVCs |
| `port` | int32 | 9092 | Broker listen port |

### Topic Spec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | *required* | Topic name on the broker |
| `numPartitions` | uint32 | *required* | Number of partitions |
| `replicationFactor` | uint32 | 0 | Replication factor |
| `brokerRef` | string | *required* | Name of Broker CR in same namespace |
| `seederImage` | string | `ghcr.io/jmpargana/musil-seeder:0.1.5` | Seeder container image |

## Development

```bash
# Install CRDs
make install

# Run operator locally
make run

# Run tests (requires envtest binaries)
make test

# Build container image
make docker-build IMG=<registry>/mink:tag
```

## Architecture

```
Broker CR ──► BrokerReconciler ──► StatefulSet + Headless Service + ConfigMap
                                          │
                                    Pod Ready?
                                          │
                                    Status.URL set
                                          │
Topic CR  ──► TopicReconciler  ──► ConfigMap (seeder.toml) + Job (musil-seeder)
                                          │
                                    Job Complete?
                                          │
                                    Topic Ready ✓
```

## License

Apache License 2.0
