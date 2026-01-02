# Sveltos OCM Addon

An Open Cluster Management (OCM) addon that integrates OCM managed clusters with Sveltos for multi-cluster management. This addon automatically creates SveltosCluster resources on the hub cluster using managed-serviceaccount for secure, token-based authentication.

## Overview

The Sveltos OCM Addon bridges OCM and Sveltos by:

- Registering OCM managed clusters as Sveltos clusters
- Using OCM's managed-serviceaccount for secure token management and automatic rotation
- Syncing labels from ManagedCluster to SveltosCluster resources
- Leveraging OCM's addon lifecycle management for deployment

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              Hub Cluster                                 │
│                                                                          │
│  ┌──────────────────────┐     ┌─────────────────────────────────────┐   │
│  │ ClusterManagementAddOn│     │         Placement                   │   │
│  │   sveltos-ocm-addon  │────▶│  (selects which clusters get addon) │   │
│  └──────────────────────┘     └─────────────────────────────────────┘   │
│            │                                    │                        │
│            │ references                         │ selects clusters       │
│            ▼                                    ▼                        │
│  ┌──────────────────────┐     ┌─────────────────────────────────────┐   │
│  │   SveltosOCMCluster  │     │  ManagedClusterAddOn (per cluster)  │   │
│  │   (configuration)    │     │  created by addon-manager           │   │
│  └──────────────────────┘     └─────────────────────────────────────┘   │
│            │                                    │                        │
│            │ config (namespace, token, labels)  │ triggers controller    │
│            ▼                                    ▼                        │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                  Sveltos OCM Controller                           │   │
│  │  - Creates ManagedServiceAccount per cluster                      │   │
│  │  - Creates kubeconfig Secret in projectsveltos namespace          │   │
│  │  - Creates SveltosCluster in projectsveltos namespace             │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### Key Design Decisions

- **No agent on managed clusters**: This addon runs entirely on the hub cluster
- **Managed-serviceaccount integration**: Uses OCM's managed-serviceaccount addon for token generation and rotation
- **Placement-based cluster selection**: Uses OCM Placements to define which clusters receive Sveltos integration
- **Configuration via SveltosOCMCluster**: A single CRD provides configuration (not cluster selection)

## Prerequisites

1. **OCM Hub Cluster** (v0.15.0+) with at least one managed cluster registered

2. **Managed-ServiceAccount Addon** (v0.7.0+)
   ```bash
   helm repo add ocm https://open-cluster-management.io/helm-charts
   helm install managed-serviceaccount ocm/managed-serviceaccount \
     --namespace open-cluster-management-addon \
     --create-namespace
   ```

3. **Sveltos** (v0.46.0+) - CRDs installed on hub cluster
   ```bash
   kubectl apply -f https://raw.githubusercontent.com/projectsveltos/libsveltos/main/config/crd/bases/lib.projectsveltos.io_sveltosclusters.yaml
   ```

## Installation

### 1. Install CRDs

```bash
make install
```

### 2. Build and Push Image

```bash
export IMG=<your-registry>/sveltos-ocm-addon:latest
make docker-build docker-push IMG=${IMG}
```

### 3. Deploy the Controller

```bash
make deploy IMG=${IMG}
```

### 4. Install OCM Resources

```bash
kubectl apply -k config/ocm
```

This creates:
- `Placement/sveltos-ocm`: Selects all clusters from the global ManagedClusterSet
- `ClusterManagementAddOn/sveltos-ocm-addon`: Defines the addon referencing the placement
- `SveltosOCMCluster/default`: Default configuration

> **Note**: Ensure a `ManagedClusterSetBinding` for the `global` ManagedClusterSet exists in the `open-cluster-management-addon` namespace. If not, create it:
> ```bash
> kubectl apply -f - <<EOF
> apiVersion: cluster.open-cluster-management.io/v1beta2
> kind: ManagedClusterSetBinding
> metadata:
>   name: global
>   namespace: open-cluster-management-addon
> spec:
>   clusterSet: global
> EOF
> ```

## Selecting Which Clusters Get Sveltos Integration

By default, the addon is installed on **all managed clusters** using the `sveltos-ocm` Placement in `open-cluster-management-addon` namespace.

To select specific clusters, you need to create a custom Placement.

### Option 1: Select clusters by label

```yaml
# 1. Create a ManagedClusterSetBinding to allow access to clusters
apiVersion: cluster.open-cluster-management.io/v1beta2
kind: ManagedClusterSetBinding
metadata:
  name: default
  namespace: sveltos-ocm-addon
spec:
  clusterSet: default

---
# 2. Create a Placement with label selector
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
```

Then modify the ClusterManagementAddOn to use your Placement:

```yaml
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
    - name: sveltos-enabled-clusters      # Your custom Placement
      namespace: sveltos-ocm-addon
      configs:
      - group: sveltos.open-cluster-management.io
        resource: sveltosocmclusters
        name: default
        namespace: open-cluster-management
```

Finally, label the clusters you want to integrate:

```bash
kubectl label managedcluster <cluster-name> sveltos-enabled=true
```

### Option 2: Select clusters by environment

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Placement
metadata:
  name: production-clusters
  namespace: sveltos-ocm-addon
spec:
  predicates:
    - requiredClusterSelector:
        labelSelector:
          matchExpressions:
            - key: environment
              operator: In
              values: ["production", "staging"]
```

### Option 3: Select specific number of clusters

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Placement
metadata:
  name: limited-clusters
  namespace: sveltos-ocm-addon
spec:
  numberOfClusters: 3
  predicates:
    - requiredClusterSelector:
        labelSelector:
          matchLabels:
            sveltos-enabled: "true"
```

## Configuration

The `SveltosOCMCluster` resource provides configuration for the addon. It does **not** select which clusters receive the addon (that's done via Placements).

```yaml
apiVersion: sveltos.open-cluster-management.io/v1alpha1
kind: SveltosOCMCluster
metadata:
  name: default
  namespace: open-cluster-management
spec:
  # Namespace where SveltosCluster resources will be created
  # Must be "projectsveltos" (hardcoded in Sveltos)
  sveltosNamespace: projectsveltos

  # Token validity period (golang duration string)
  # Default: 168h (7 days)
  tokenValidity: "168h"

  # Sync labels from ManagedCluster to SveltosCluster
  # Default: true
  labelSync: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `sveltosNamespace` | string | `projectsveltos` | Namespace for SveltosCluster resources (must be `projectsveltos`) |
| `tokenValidity` | duration | `168h` | Token validity period |
| `labelSync` | bool | `true` | Sync labels from ManagedCluster to SveltosCluster |

## How It Works

1. **Placement selects clusters**: OCM's Placement API determines which managed clusters receive the addon
2. **addon-manager creates ManagedClusterAddOn**: For each selected cluster, a ManagedClusterAddOn is created in the cluster's namespace
3. **Controller watches ManagedClusterAddOns**: The sveltos-ocm-addon controller detects new addons
4. **For each cluster, the controller**:
   - Creates a ManagedServiceAccount to obtain a service account token
   - Waits for the token to be provisioned
   - Fetches the ManagedCluster to get API server URL
   - Creates a kubeconfig secret in `projectsveltos` namespace
   - Creates a SveltosCluster resource pointing to the kubeconfig
   - Syncs labels from ManagedCluster to SveltosCluster (if enabled)

## Monitoring

### Check registered clusters

```bash
kubectl get sveltosocmcluster default -n open-cluster-management -o yaml
```

Example status:

```yaml
status:
  conditions:
  - type: Available
    status: "True"
    reason: ClustersRegistered
    message: Successfully registered 3 cluster(s)
  registeredClusters:
  - clusterName: cluster1
    clusterNamespace: cluster1
    sveltosClusterCreated: true
    tokenSecretRef: sveltos-ocm-token
    expirationTime: "2025-01-22T10:00:00Z"
```

### Check SveltosCluster resources

```bash
kubectl get sveltosclusters -n projectsveltos
```

### Check controller logs

```bash
kubectl logs -n sveltos-ocm-addon-system deployment/sveltos-ocm-addon-controller-manager
```

## Troubleshooting

### Addon not creating SveltosCluster

1. Check if ManagedClusterAddOn exists:
   ```bash
   kubectl get managedclusteraddon sveltos-ocm-addon -n <cluster-namespace>
   ```

2. Check Placement decisions:
   ```bash
   kubectl get placementdecisions -n <placement-namespace>
   ```

3. Check ManagedServiceAccount status:
   ```bash
   kubectl get managedserviceaccount sveltos-ocm -n <cluster-namespace> -o yaml
   ```

### Token not ready

1. Verify managed-serviceaccount addon is running:
   ```bash
   kubectl get pods -n open-cluster-management-addon
   ```

2. Check klusterlet status on managed cluster

### Cluster not selected by Placement

1. Check cluster labels:
   ```bash
   kubectl get managedcluster <cluster-name> --show-labels
   ```

2. Verify ManagedClusterSetBinding exists in Placement namespace

3. Check Placement status:
   ```bash
   kubectl get placement <name> -n <namespace> -o yaml
   ```

## Development

### Running Locally

```bash
make install
make run
```

### Running Tests

```bash
make test
```

### Regenerating Manifests

```bash
make manifests
```

## Uninstalling

```bash
kubectl delete -k config/ocm
make undeploy
make uninstall
```

## References

- [Open Cluster Management](https://open-cluster-management.io/)
- [OCM Placement](https://open-cluster-management.io/docs/concepts/content-placement/placement/)
- [Sveltos](https://projectsveltos.github.io/sveltos/)
- [Managed ServiceAccount Addon](https://github.com/open-cluster-management-io/managed-serviceaccount)

## License

Apache License 2.0
