# mink

A Helm chart for deploying the MINK Kubernetes operator for Musil message brokers.

## Prerequisites

- Kubernetes 1.28+
- Helm 3.8+ (OCI support)

## Installation

```bash
helm install mink oci://ghcr.io/jmpargana/charts/mink --version 0.1.0
```

## Upgrading CRDs

Helm does not upgrade CRDs automatically. After upgrading the chart, apply CRDs manually:

```bash
kubectl apply -f https://raw.githubusercontent.com/jmpargana/mink/v<VERSION>/charts/mink/crds/mink.io.musil_brokers.yaml
kubectl apply -f https://raw.githubusercontent.com/jmpargana/mink/v<VERSION>/charts/mink/crds/mink.io.musil_topics.yaml
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| replicaCount | int | `1` | Number of operator replicas |
| image.repository | string | `"ghcr.io/jmpargana/mink"` | Operator image repository |
| image.pullPolicy | string | `"IfNotPresent"` | Operator image pull policy |
| image.tag | string | `""` | Overrides the image tag (default is the chart appVersion) |
| imagePullSecrets | list | `[]` | Image pull secrets |
| nameOverride | string | `""` | Override the chart name |
| fullnameOverride | string | `""` | Override the full release name |
| serviceAccount.create | bool | `true` | Create a ServiceAccount |
| serviceAccount.annotations | object | `{}` | Annotations for the ServiceAccount |
| serviceAccount.name | string | `""` | Override ServiceAccount name |
| podAnnotations | object | `{}` | Pod annotations |
| podSecurityContext | object | restricted PSS | Pod security context |
| securityContext | object | hardened | Container security context |
| resources.limits.cpu | string | `"500m"` | CPU limit |
| resources.limits.memory | string | `"128Mi"` | Memory limit |
| resources.requests.cpu | string | `"10m"` | CPU request |
| resources.requests.memory | string | `"64Mi"` | Memory request |
| nodeSelector | object | `{}` | Node selector |
| tolerations | list | `[]` | Tolerations |
| affinity | object | `{}` | Affinity rules |
| priorityClassName | string | `""` | Priority class name |
| leaderElection.enabled | bool | `true` | Enable leader election |
| networkPolicy.enabled | bool | `false` | Enable NetworkPolicy for the operator |
| broker.enabled | bool | `false` | Deploy an optional Broker CR |
| broker.spec | object | `{}` | Full Broker CR spec (passthrough) |
| topic.enabled | bool | `false` | Deploy an optional Topic CR |
| topic.spec | object | `{}` | Full Topic CR spec (passthrough) |

## Production Values Example

```yaml
replicaCount: 1

resources:
  limits:
    cpu: "1"
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi

affinity:
  nodeAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        preference:
          matchExpressions:
            - key: node-role.kubernetes.io/control-plane
              operator: Exists

networkPolicy:
  enabled: true

broker:
  enabled: true
  spec:
    storageSize: "10Gi"
    replicas: 3
    port: 9092
    storageClassName: fast-ssd

topic:
  enabled: true
  spec:
    name: "events"
    numPartitions: 6
    replicationFactor: 3
    brokerRef: "mink-broker"
```
