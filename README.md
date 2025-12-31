# Sveltos OCM Addon

An idiomatic Open Cluster Management (OCM) addon that integrates OCM managed clusters with Sveltos for advanced multi-cluster management. This addon automatically creates SveltosCluster resources on the hub cluster using managed-serviceaccount for secure, token-based authentication.

## Overview

The Sveltos OCM Addon bridges OCM and Sveltos by:

- Automatically registering OCM managed clusters as Sveltos clusters
- Using OCM's managed-serviceaccount for secure token management and automatic rotation
- Syncing labels from ManagedCluster to SveltosCluster resources
- Leveraging OCM's addon lifecycle management for deployment

## Architecture

This addon follows the OCM addon pattern with some key design decisions:

- **No agent on managed clusters**: Unlike traditional OCM addons, this addon runs entirely on the hub cluster
- **Managed-serviceaccount integration**: Uses OCM's managed-serviceaccount addon for token generation and rotation
- **Declarative lifecycle**: Utilizes ClusterManagementAddOn with the addon-manager lifecycle annotation
- **Hub-side controller**: Watches ManagedClusterAddOn resources and creates corresponding SveltosCluster resources

### How it Works

1. ClusterManagementAddOn is installed on the hub cluster with addon-manager lifecycle
2. OCM's addon-manager automatically creates ManagedClusterAddOn resources in managed cluster namespaces
3. The Sveltos OCM controller watches for these ManagedClusterAddOn resources
4. For each managed cluster, the controller:
   - Creates a ManagedServiceAccount to obtain a service account token
   - Waits for the token to be provisioned
   - Fetches the ManagedCluster to get API server URL and CA certificate
   - Creates a kubeconfig secret with the token
   - Creates a SveltosCluster resource pointing to the kubeconfig
   - Optionally syncs labels from ManagedCluster to SveltosCluster

## Prerequisites

### Required Components

1. **OCM Hub Cluster** (v0.15.0+)
   - Open Cluster Management hub components installed
   - At least one managed cluster registered

2. **Managed-ServiceAccount Addon** (v0.7.0+)
   ```bash
   # Install using the OCM addon framework
   helm repo add ocm https://open-cluster-management.io/helm-charts
   helm install managed-serviceaccount ocm/managed-serviceaccount \
     --namespace open-cluster-management-addon \
     --create-namespace
   ```

3. **Sveltos** (v0.46.0+)
   - Sveltos CRDs should be installed on the hub cluster
   ```bash
   kubectl apply -f https://raw.githubusercontent.com/projectsveltos/libsveltos/main/config/crd/bases/lib.projectsveltos.io_sveltosclusters.yaml
   ```

4. **Kubernetes** v1.11.3+
5. **Go** v1.24.6+ (for development)
6. **Docker** 17.03+ (for building images)
7. **kubectl** v1.11.3+

### RBAC Requirements

The addon requires cluster-admin or equivalent permissions to:
- Read ManagedClusterAddOn and ManagedCluster resources
- Create and manage ManagedServiceAccount resources
- Create and manage SveltosCluster resources
- Create and manage Secrets in the Sveltos namespace

## Installation

### 1. Install CRDs

```bash
make install
```

This installs the SveltosOCMCluster CRD on the hub cluster.

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

Install the ClusterManagementAddOn and default SveltosOCMCluster configuration:

```bash
kubectl apply -k config/ocm
```

This creates:
- `ClusterManagementAddOn/sveltos-ocm-addon`: Defines the addon
- `SveltosOCMCluster/default`: Default configuration for the addon

### 5. Verify Installation

Check that the addon is registered:

```bash
kubectl get clustermanagementaddon sveltos-ocm-addon
```

Verify ManagedClusterAddOn resources are created in managed cluster namespaces:

```bash
kubectl get managedclusteraddon -A
```

Check SveltosCluster resources are being created:

```bash
kubectl get sveltosclusters -n sveltos
```

## Configuration

The addon is configured using the `SveltosOCMCluster` custom resource:

```yaml
apiVersion: sveltos.open-cluster-management.io/v1alpha1
kind: SveltosOCMCluster
metadata:
  name: default
  namespace: open-cluster-management-global-set
spec:
  # Namespace where SveltosCluster resources will be created
  # Default: sveltos
  sveltosNamespace: sveltos

  # Token validity period (golang duration string)
  # Default: 168h (7 days)
  tokenValidity: "168h"

  # Sync labels from ManagedCluster to SveltosCluster
  # Default: true
  labelSync: true
```

### Configuration Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `sveltosNamespace` | string | `sveltos` | Namespace where SveltosCluster resources will be created |
| `tokenValidity` | string | `168h` | Token validity period (golang duration format) |
| `labelSync` | bool | `true` | Whether to sync labels from ManagedCluster to SveltosCluster |

## Usage

### Registering Clusters

Clusters are automatically registered when they join OCM. The addon-manager will create a ManagedClusterAddOn resource for each managed cluster, and the controller will:

1. Create a ManagedServiceAccount
2. Wait for the token to be ready
3. Create a kubeconfig secret
4. Create a SveltosCluster resource

### Monitoring Status

Check the status of the SveltosOCMCluster:

```bash
kubectl get sveltosocmcluster default -n open-cluster-management-global-set -o yaml
```

The status shows:
- Registered clusters
- Token references
- Token expiration times
- Conditions

Example status:

```yaml
status:
  conditions:
  - lastTransitionTime: "2025-01-15T10:00:00Z"
    message: Successfully registered 3 cluster(s)
    observedGeneration: 1
    reason: ClustersRegistered
    status: "True"
    type: Available
  registeredClusters:
  - clusterName: cluster1
    clusterNamespace: cluster1
    expirationTime: "2025-01-22T10:00:00Z"
    sveltosClusterCreated: true
    tokenSecretRef: cluster1-managed-sa-token
  - clusterName: cluster2
    clusterNamespace: cluster2
    expirationTime: "2025-01-22T10:00:00Z"
    sveltosClusterCreated: true
    tokenSecretRef: cluster2-managed-sa-token
```

### Token Rotation

Token rotation is handled automatically by the managed-serviceaccount addon. The controller will detect token updates and refresh the kubeconfig secrets accordingly.

### Removing Clusters

When a managed cluster is removed from OCM, the corresponding:
- ManagedClusterAddOn is deleted
- ManagedServiceAccount is cleaned up
- SveltosCluster is removed
- Kubeconfig secret is deleted

## Troubleshooting

### Addon Not Creating SveltosCluster

1. Check if ManagedClusterAddOn exists:
   ```bash
   kubectl get managedclusteraddon sveltos-ocm-addon -n <cluster-namespace>
   ```

2. Check ManagedServiceAccount status:
   ```bash
   kubectl get managedserviceaccount -n <cluster-namespace>
   ```

3. Check controller logs:
   ```bash
   kubectl logs -n sveltos-ocm-addon-system deployment/sveltos-ocm-addon-controller-manager
   ```

### Token Not Ready

If the ManagedServiceAccount token is not becoming ready:

1. Verify managed-serviceaccount addon is running:
   ```bash
   kubectl get pods -n open-cluster-management-addon
   ```

2. Check ManagedServiceAccount status:
   ```bash
   kubectl get managedserviceaccount <name> -n <namespace> -o yaml
   ```

3. Ensure the managed cluster's klusterlet is healthy:
   ```bash
   kubectl get klusterlet -o yaml
   ```

### SveltosCluster Not Working

1. Verify the kubeconfig secret exists:
   ```bash
   kubectl get secret <cluster-name>-kubeconfig -n sveltos
   ```

2. Test the kubeconfig manually:
   ```bash
   kubectl get secret <cluster-name>-kubeconfig -n sveltos -o jsonpath='{.data.value}' | base64 -d > /tmp/kubeconfig
   kubectl --kubeconfig=/tmp/kubeconfig get nodes
   ```

3. Check Sveltos controller logs for errors.

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

### Generating Manifests

After modifying API types, regenerate manifests:

```bash
make manifests
```

### Updating RBAC

RBAC is generated from kubebuilder markers in the controller. After adding new resource types, run:

```bash
make manifests
```

## Uninstalling

### Remove the Addon

```bash
kubectl delete -k config/ocm
```

### Uninstall the Controller

```bash
make undeploy
```

### Remove CRDs

```bash
make uninstall
```

## Architecture Details

### Controller Reconciliation Loop

1. List all ManagedClusterAddOn resources with name `sveltos-ocm-addon`
2. For each addon (representing a managed cluster):
   - Ensure ManagedServiceAccount exists
   - Wait for token to be provisioned
   - Fetch ManagedCluster for API server details
   - Create/update kubeconfig secret
   - Create/update SveltosCluster
   - Sync labels if enabled
3. Update SveltosOCMCluster status with registered clusters

### Resource Ownership

- ManagedServiceAccount resources are owned by SveltosOCMCluster (controller reference)
- Kubeconfig secrets are not owned to prevent cascading deletion
- SveltosCluster resources are not owned to allow independent lifecycle

### Finalizers

The controller uses a finalizer (`sveltos.open-cluster-management.io/finalizer`) to ensure clean deletion:

1. When SveltosOCMCluster is deleted, the controller:
   - Deletes all ManagedServiceAccount resources
   - Deletes all SveltosCluster resources
   - Deletes all kubeconfig secrets
   - Removes the finalizer

## Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

## References

- [Open Cluster Management](https://open-cluster-management.io/)
- [Sveltos](https://projectsveltos.github.io/sveltos/)
- [Managed ServiceAccount Addon](https://github.com/open-cluster-management-io/managed-serviceaccount)
- [OCM Addon Framework](https://open-cluster-management.io/concepts/addon/)
