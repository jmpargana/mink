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

// TopicSpec defines the desired state of Topic.
type TopicSpec struct {
	// +required
	Name string `json:"name"`

	// Number of partitions for this topic.
	// NOTE: updating this field after initial creation has no effect — musil
	// does not yet expose an RPC to alter partition counts on an existing topic.
	// +required
	// +kubebuilder:default=2
	NumPartitions uint32 `json:"numPartitions"`

	// +optional
	ReplicationFactor uint32 `json:"replicationFactor,omitempty"`

	// Name of the Broker CR in the same namespace to seed this topic into.
	// +required
	BrokerRef string `json:"brokerRef"`

	// +optional
	// +kubebuilder:default="ghcr.io/jmpargana/musil-seeder:0.1.5"
	SeederImage string `json:"seederImage,omitempty"`
}

// TopicStatus defines the observed state of Topic.
type TopicStatus struct {
	// Number of times the controller has recreated a failed seeder Job.
	// +optional
	// +kubebuilder:default=3
	RetryCount int32 `json:"retryCount,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Topic is the Schema for the topics API.
type Topic struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec TopicSpec `json:"spec"`

	// +optional
	Status TopicStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TopicList contains a list of Topic.
type TopicList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Topic `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Topic{}, &TopicList{})
		return nil
	})
}
