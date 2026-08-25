/*
Copyright 2026.

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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// BrokerSpec defines the desired state of Broker.
type BrokerSpec struct {
	// +optional
	// +kubebuilder:default="ghcr.io/jmpargana/musil-broker:0.1.5"
	Image string `json:"image,omitempty"`

	// +optional
	// +kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`

	// Storage Size is a weird variable. It needs to be coordinated with the storage size in musil itself.
	// Default is 1GB, but actually that's also the segment default size, so full features won't be available straight away.
	// +required
	StorageSize string `json:"storageSize"`

	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// +optional
	// +kubebuilder:default=9092
	Port int32 `json:"port,omitempty"`
}

// BrokerStatus defines the observed state of Broker.
type BrokerStatus struct {
	// Fully qualified cluster DNS address for the broker service.
	// Written only when the StatefulSet has at least one ready replica.
	// +optional
	URL string `json:"url,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Broker is the Schema for the brokers API.
type Broker struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec BrokerSpec `json:"spec"`

	// +optional
	Status BrokerStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BrokerList contains a list of Broker.
type BrokerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Broker `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Broker{}, &BrokerList{})
		return nil
	})
}
