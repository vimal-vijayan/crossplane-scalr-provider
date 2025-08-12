package v1alpha1

import (
	"reflect"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type WorkspaceParameters struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	Name *string `json:"name"`
	// +kubebuilder:validation:Optional
	Description *string `json:"description,omitempty"`
	// +kubebuilder:validation:Required
	EnvironmentName *string `json:"environmentName"`
	// +kubebuilder:validation:Optional
	Tags map[string]string `json:"tags,omitempty"`
	// +kubebuilder:validation:Optional
	Provider map[string]string `json:"provider,omitempty"`
}

type WorkspaceSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       WorkspaceParameters `json:"forProvider"`
}

type WorkspaceObservation struct {
	WorkspaceId *string `json:"workspaceId,omitempty"`
}

// A WorkspaceStatus represents the observed state of a Workspace.
type WorkspaceStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          WorkspaceObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,scalr}

// Workspace is the Schema for the workspaces API. Workspace defines a Scalr workspace resource.
type Workspace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkspaceSpec   `json:"spec"`
	Status WorkspaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type WorkspaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workspace `json:"items"`
}

var (
	WorkspaceKind             = reflect.TypeOf(Workspace{}).Name()
	WorkspaceGroupKind        = schema.GroupKind{Group: SchemeGroupVersion.Group, Kind: WorkspaceKind}
	WorkspaceKindAPIVersion   = WorkspaceKind + "." + SchemeGroupVersion.String()
	WorkspaceGroupVersionKind = SchemeGroupVersion.WithKind(WorkspaceKind)
)

func init() {
	SchemeBuilder.Register(&Workspace{}, &WorkspaceList{})
}
