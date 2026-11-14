package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CredentialsSecretReference references the Secret holding the credentials the
// operator uses to talk to the Zitadel management API.
type CredentialsSecretReference struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Namespace of the Secret.
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
	// Key in the Secret. Its content is either a service account JSON key
	// (as downloaded from the Zitadel console, recommended) or a personal access token.
	// +kubebuilder:default="key.json"
	// +optional
	Key string `json:"key,omitempty"`
}

// ZitadelConnectionSpec defines how to reach a Zitadel instance.
type ZitadelConnectionSpec struct {
	// URL is the public URL (issuer) of the Zitadel instance, e.g. https://auth.example.com.
	// It is used as the OIDC issuer written in application Secrets.
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`

	// InternalURL is an optional endpoint used for API calls instead of URL, for
	// example the in-cluster service (http://zitadel.zitadel.svc.cluster.local:8080).
	// The Host header is still set to the host of URL so Zitadel resolves the instance.
	// +kubebuilder:validation:Pattern=`^https?://`
	// +optional
	InternalURL string `json:"internalURL,omitempty"`

	// OrganizationID is the default organization in which resources are created.
	// Defaults to the organization of the service account.
	// +optional
	OrganizationID string `json:"organizationId,omitempty"`

	// CredentialsSecretRef references the credentials of a Zitadel service account
	// with either the ORG_OWNER role on the organization (projects are created by
	// the operator) or the PROJECT_OWNER role on existing projects (least privilege).
	CredentialsSecretRef CredentialsSecretReference `json:"credentialsSecretRef"`

	// InsecureSkipTLSVerify disables TLS certificate verification. Do not use in production.
	// +optional
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`

	// AllowedNamespaces restricts the namespaces whose ZitadelProject /
	// ZitadelApplication may use this connection. Empty means all namespaces.
	// Entries support shell-like globs ("team-*").
	// +optional
	AllowedNamespaces []string `json:"allowedNamespaces,omitempty"`
}

// ZitadelConnectionStatus defines the observed state of ZitadelConnection.
type ZitadelConnectionStatus struct {
	// OrganizationID effectively used by default.
	// +optional
	OrganizationID string `json:"organizationId,omitempty"`
	// OrganizationName of the default organization.
	// +optional
	OrganizationName string `json:"organizationName,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=zcon,categories=zitadel
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Organization",type=string,JSONPath=`.status.organizationName`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ZitadelConnection is a cluster-scoped connection to a Zitadel instance. It
// holds the admin credentials so that application namespaces never see them.
type ZitadelConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZitadelConnectionSpec   `json:"spec,omitempty"`
	Status ZitadelConnectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ZitadelConnectionList contains a list of ZitadelConnection.
type ZitadelConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ZitadelConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ZitadelConnection{}, &ZitadelConnectionList{})
}
