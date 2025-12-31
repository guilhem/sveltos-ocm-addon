/*
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
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SveltosOCMClusterSpec defines the desired state of SveltosOCMCluster
type SveltosOCMClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// SveltosNamespace is the namespace where SveltosCluster resources will be created
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:default=sveltos
	// +optional
	SveltosNamespace string `json:"sveltosNamespace,omitempty"`

	// TokenValidity specifies the validity period for the managed service account token
	// Format: golang duration string (e.g., "24h", "168h")
	// +optional
	TokenValidity metav1.Duration `json:"tokenValidity,omitempty"`

	// LabelSync specifies whether to sync labels from ManagedCluster to SveltosCluster
	// +kubebuilder:default=true
	// +optional
	LabelSync bool `json:"labelSync"`
}

// RegisteredClusterInfo contains information about a registered cluster
type RegisteredClusterInfo struct {
	// ClusterName is the name of the managed cluster
	ClusterName string `json:"clusterName"`

	// ClusterNamespace is the namespace of the managed cluster
	ClusterNamespace string `json:"clusterNamespace"`

	// TokenSecretRef references the secret containing the service account token
	// +optional
	TokenSecretRef *string `json:"tokenSecretRef,omitempty"`

	// ExpirationTime is the expiration time of the token
	// +optional
	ExpirationTime *metav1.Time `json:"expirationTime,omitempty"`

	// SveltosClusterCreated indicates if the SveltosCluster has been created
	// +optional
	SveltosClusterCreated bool `json:"sveltosClusterCreated,omitempty"`
}

// SveltosOCMClusterStatus defines the observed state of SveltosOCMCluster.
type SveltosOCMClusterStatus struct {
	// conditions represent the current state of the SveltosOCMCluster resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// RegisteredClusters lists all managed clusters registered with their Sveltos clusters
	// +optional
	// +listType=map
	// +listMapKey=clusterName
	RegisteredClusters []RegisteredClusterInfo `json:"registeredClusters,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// SveltosOCMCluster is the Schema for the sveltosocmclusters API
type SveltosOCMCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of SveltosOCMCluster
	// +required
	Spec SveltosOCMClusterSpec `json:"spec"`

	// status defines the observed state of SveltosOCMCluster
	// +optional
	Status SveltosOCMClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// SveltosOCMClusterList contains a list of SveltosOCMCluster
type SveltosOCMClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []SveltosOCMCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SveltosOCMCluster{}, &SveltosOCMClusterList{})
}
