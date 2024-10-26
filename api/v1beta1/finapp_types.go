/*
Copyright 2024.

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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// FinAppSpec defines the desired state of FinApp
type FinAppSpec struct {
	// DeploymentConfig holds the configuration for the deployment
	DeploymentConfig DeploymentConfig `json:"deploymentConfig,omitempty"`

	// PrometheusConfig holds the configuration for Prometheus
	PrometheusConfig PrometheusConfig `json:"prometheusConfig,omitempty"`
}

// PrometheusConfig defines the configuration for Prometheus
type PrometheusConfig struct {
	// Add fields for Prometheus configuration
	Endpoint string `json:"endpoint,omitempty"`
	// Add other necessary fields
}

// DeploymentConfig defines the configuration for the deployment
type DeploymentConfig struct {
	// Add fields for deployment configuration
	Replicas int32  `json:"replicas,omitempty"`
	Image    string `json:"image,omitempty"`
	// Add other necessary fields
}

// ResourcePrediction defines the resource prediction for a specific hour
type ResourcePrediction struct {
	Hour   int    `json:"hour,omitempty"`
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// FinAppStatus defines the observed state of FinApp
type FinAppStatus struct {
	Cronjob            string       `json:"cronjob,omitempty"`
	LastPredictionTime *metav1.Time `json:"lastPredictionTime,omitempty"`
	// Predictions holds the hourly resource predictions
	Predictions []ResourcePrediction `json:"predictions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// FinApp is the Schema for the finapps API
type FinApp struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FinAppSpec   `json:"spec"`
	Status FinAppStatus `json:"status"`
}

//+kubebuilder:object:root=true

// FinAppList contains a list of FinApp
type FinAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FinApp `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FinApp{}, &FinAppList{})
}
