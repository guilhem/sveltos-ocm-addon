# Sveltos OCM Addon

Integrates OCM managed clusters with Sveltos via cluster-proxy. No agent deployed on managed clusters.

## Prerequisites

- OCM Hub (v0.15.0+)
- [cluster-proxy](https://open-cluster-management.io/docs/concepts/addon/#cluster-proxy) with `enableServiceProxy=true`
- [managed-serviceaccount](https://open-cluster-management.io/getting-started/integration/managed-serviceaccount/) addon (v0.7.0+)
- Sveltos CRDs on the hub

```bash
kubectl apply -f https://raw.githubusercontent.com/projectsveltos/libsveltos/main/config/crd/bases/lib.projectsveltos.io_sveltosclusters.yaml
```

## Installation

```bash
# CRDs
make install

# Controller
export IMG=<your-registry>/sveltos-ocm-addon:latest
make docker-build docker-push deploy IMG=${IMG}

# OCM resources (Placement, ClusterManagementAddOn, SveltosOCMCluster)
kubectl apply -k config/ocm
```

## Configuration

```yaml
apiVersion: sveltos.open-cluster-management.io/v1alpha1
kind: SveltosOCMCluster
metadata:
  name: default
  namespace: open-cluster-management
spec:
  tokenValidity: "168h"  # Token validity (default: 7 days)
  labelSync: true        # Sync labels ManagedCluster -> SveltosCluster
  shard: ""              # Sveltos shard (optional)
```

## Cluster Selection

To filter clusters, create a `Placement` and reference it in the `ClusterManagementAddOn`:

```yaml
# 1. Namespace + ManagedClusterSetBinding
apiVersion: v1
kind: Namespace
metadata:
  name: sveltos-ocm-addon
---
apiVersion: cluster.open-cluster-management.io/v1beta2
kind: ManagedClusterSetBinding
metadata:
  name: default
  namespace: sveltos-ocm-addon
spec:
  clusterSet: default
---
# 2. Placement
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Placement
metadata:
  name: sveltos-enabled-clusters
  namespace: sveltos-ocm-addon
spec:
  predicates:
    - requiredClusterSelector:
        labelSelector:
          matchLabels:
            sveltos-enabled: "true"
---
# 3. ClusterManagementAddOn referencing the Placement
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ClusterManagementAddOn
metadata:
  name: sveltos-ocm-addon
  annotations:
    addon.open-cluster-management.io/lifecycle: "addon-manager"
spec:
  addOnMeta:
    displayName: Sveltos OCM Integration
    description: Registers OCM managed clusters with Sveltos
  supportedConfigs:
    - group: sveltos.open-cluster-management.io
      resource: sveltosocmclusters
  installStrategy:
    type: Placements
    placements:
      - name: sveltos-enabled-clusters        # References the Placement above
        namespace: sveltos-ocm-addon
        configs:
          - group: sveltos.open-cluster-management.io
            resource: sveltosocmclusters
            name: default
            namespace: open-cluster-management
```

Then label clusters: `kubectl label managedcluster <name> sveltos-enabled=true`

## Monitoring

```bash
# Global status
kubectl get sveltosocmcluster default -n open-cluster-management -o yaml

# Created SveltosClusters
kubectl get sveltosclusters -A

# Logs
kubectl logs -n sveltos-ocm-addon-system deploy/sveltos-ocm-addon-controller-manager
```

## Troubleshooting

| Issue | Check |
|-------|-------|
| Addon not installed | `kubectl get managedclusteraddon sveltos-ocm-addon -n <cluster>` |
| Token not ready | `kubectl get managedserviceaccount sveltos-ocm -n <cluster>` |
| Cluster not selected | `kubectl get placementdecisions -n open-cluster-management-addon` |

## Uninstall

```bash
kubectl delete -k config/ocm
make undeploy uninstall
```

---

## Architecture

```mermaid
flowchart TB
    subgraph Hub["Hub Cluster"]
        CMA[ClusterManagementAddOn<br/>sveltos-ocm-addon]
        Placement[Placement]
        SOC[SveltosOCMCluster<br/>configuration]

        subgraph Controller["sveltos-ocm-addon controller"]
            Reconciler[Reconciler]
        end

        subgraph PerCluster["Per managed cluster (namespace: cluster-name)"]
            MCA[ManagedClusterAddOn]
            MSA[ManagedServiceAccount]
            MW[ManifestWork<br/>RBAC]
            Secret[kubeconfig Secret]
            SC[SveltosCluster]
        end

        CMA --> Placement
        Placement -->|selects| MCA
        SOC -->|config| Reconciler
        MCA -->|triggers| Reconciler
        Reconciler -->|creates| MSA
        Reconciler -->|creates| MW
        Reconciler -->|creates| Secret
        Reconciler -->|creates| SC
    end

    subgraph Managed["Managed Cluster"]
        SA[ServiceAccount<br/>sveltos-ocm]
        RBAC[ClusterRole/Binding]
    end

    subgraph Proxy["cluster-proxy"]
        CP[cluster-proxy-addon-user]
    end

    MSA -.->|generates token| SA
    MW -.->|deploys| RBAC
    SC -->|via| CP
    CP -->|tunnel| Managed
```

### Reconciliation Flow

```mermaid
sequenceDiagram
    participant P as Placement
    participant AM as addon-manager
    participant C as Controller
    participant MSA as ManagedServiceAccount
    participant MC as Managed Cluster

    P->>AM: Selects cluster
    AM->>AM: Creates ManagedClusterAddOn
    AM-->>C: Triggers reconcile
    C->>MSA: Creates ManagedServiceAccount
    C->>MC: Deploys RBAC (ManifestWork)
    MSA-->>MC: Creates ServiceAccount + Token
    MSA-->>C: Token ready
    C->>C: Creates kubeconfig Secret
    C->>C: Creates SveltosCluster
```

## References

- [Open Cluster Management](https://open-cluster-management.io/)
- [Sveltos](https://projectsveltos.github.io/sveltos/)
- [cluster-proxy](https://open-cluster-management.io/docs/concepts/addon/#cluster-proxy)
- [managed-serviceaccount](https://github.com/open-cluster-management-io/managed-serviceaccount)

## License

Apache License 2.0
