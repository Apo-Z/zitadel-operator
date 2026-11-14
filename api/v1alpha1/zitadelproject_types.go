package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProjectRole is a role defined on a Zitadel project.
type ProjectRole struct {
	// Key of the role, e.g. "admin".
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
	// DisplayName of the role. Defaults to Key.
	// +optional
	DisplayName string `json:"displayName,omitempty"`
	// Group of the role.
	// +optional
	Group string `json:"group,omitempty"`
}

// ZitadelProjectSpec defines the desired state of ZitadelProject.
type ZitadelProjectSpec struct {
	// ConnectionRef references the ZitadelConnection to use.
	ConnectionRef ConnectionReference `json:"connectionRef"`

	// OrganizationID overrides the organization of the connection.
	// +optional
	OrganizationID string `json:"organizationId,omitempty"`

	// Name of the project in Zitadel. Defaults to metadata.name.
	// If a project with this name already exists in the organization, it is adopted.
	// +optional
	Name string `json:"name,omitempty"`

	// Roles managed on the project. Roles created by the operator and later
	// removed from this list are deleted from Zitadel; other roles are left untouched.
	// +listType=map
	// +listMapKey=key
	// +optional
	Roles []ProjectRole `json:"roles,omitempty"`

	// ProjectRoleAssertion adds the user roles to the tokens.
	// +optional
	ProjectRoleAssertion bool `json:"projectRoleAssertion,omitempty"`
	// ProjectRoleCheck requires users to have a role on the project to authenticate.
	// +optional
	ProjectRoleCheck bool `json:"projectRoleCheck,omitempty"`
	// HasProjectCheck requires the user's organization to be granted the project.
	// +optional
	HasProjectCheck bool `json:"hasProjectCheck,omitempty"`

	// DeletionPolicy defines what happens in Zitadel when this object is deleted.
	// Deleting a project also deletes all its applications in Zitadel.
	// +kubebuilder:default=Delete
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
}

// ZitadelProjectStatus defines the observed state of ZitadelProject.
type ZitadelProjectStatus struct {
	// ProjectID in Zitadel.
	// +optional
	ProjectID string `json:"projectId,omitempty"`
	// OrganizationID owning the project.
	// +optional
	OrganizationID string `json:"organizationId,omitempty"`
	// ConnectionName used when the project was created (used for cleanup).
	// +optional
	ConnectionName string `json:"connectionName,omitempty"`
	// ManagedRoles are the role keys created by the operator.
	// +optional
	ManagedRoles []string `json:"managedRoles,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=zproj,categories=zitadel
// +kubebuilder:printcolumn:name="Project ID",type=string,JSONPath=`.status.projectId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ZitadelProject is a project in Zitadel.
type ZitadelProject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZitadelProjectSpec   `json:"spec,omitempty"`
	Status ZitadelProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ZitadelProjectList contains a list of ZitadelProject.
type ZitadelProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ZitadelProject `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ZitadelProject{}, &ZitadelProjectList{})
}
