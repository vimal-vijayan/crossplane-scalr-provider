package v1alpha1

import (
	"reflect"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type RunnerParameters struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	Name *string `json:"name"`
	// +kubebuilder:validation:Optional
	Provider map[string]string `json:"provider,omitempty"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="opentofu"
	Driver *string `json:"driver,omitempty"`
	// +kubebuilder:validation:Required
	Source string `json:"source"`
	// +kubebuilder:validation:Optional
	Env map[string]string `json:"env,omitempty"`
	// +kubebuilder:validation:Optional
	Vars map[string]string `json:"vars,omitempty"`
}

type RunnerSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       RunnerParameters `json:"forProvider"`
}

type RunnerObservation struct {
	RunnerId *string `json:"id,omitempty"`
}

type RunnerStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          RunnerObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,iacorchestrator}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".status.atProvider.id"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase"

type Runner struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RunnerSpec   `json:"spec"`
	Status RunnerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type RunnerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Runner `json:"items"`
}

var (
	RunnerKind             = reflect.TypeOf(Runner{}).Name()
	RunnerGroupKind        = schema.GroupKind{Group: Group, Kind: RunnerKind}.String()
	RunnerKindAPIVersion   = RunnerKind + "." + SchemeGroupVersion.String()
	RunnerGroupVersion     = SchemeGroupVersion.String()
	RunnerGroupVersionKind = SchemeGroupVersion.WithKind(RunnerKind)
)

func init() {
	SchemeBuilder.Register(&Runner{}, &RunnerList{})
}
