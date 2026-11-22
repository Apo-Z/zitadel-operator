package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ZitadelSMTPProviderSpec defines the desired state of ZitadelSMTPProvider.
type ZitadelSMTPProviderSpec struct {
	// ConnectionRef references the ZitadelConnection to use. Its service account
	// needs instance permissions (IAM_OWNER).
	ConnectionRef ConnectionReference `json:"connectionRef"`

	// Description identifies the provider in Zitadel. Defaults to metadata.name.
	// An existing provider with this description (or else with the same host) is adopted.
	// +optional
	Description string `json:"description,omitempty"`

	// Host and port of the SMTP server, e.g. "smtp.example.com:465".
	// +kubebuilder:validation:MinLength=1
	Host string `json:"host"`
	// User to authenticate with.
	// +optional
	User string `json:"user,omitempty"`
	// PasswordSecretRef holds the SMTP password.
	// +optional
	PasswordSecretRef *SecretKeyReference `json:"passwordSecretRef,omitempty"`
	// TLS enables TLS.
	// +kubebuilder:default=true
	// +optional
	TLS *bool `json:"tls,omitempty"`

	// SenderAddress of the emails.
	// +kubebuilder:validation:MinLength=1
	SenderAddress string `json:"senderAddress"`
	// SenderName of the emails (required by Zitadel).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=200
	SenderName string `json:"senderName"`
	// ReplyToAddress of the emails.
	// +optional
	ReplyToAddress string `json:"replyToAddress,omitempty"`

	// Active makes it the active email provider of the instance.
	// +kubebuilder:default=true
	// +optional
	Active *bool `json:"active,omitempty"`

	// DeletionPolicy defines what happens in Zitadel when this object is deleted.
	// +kubebuilder:default=Retain
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
}

// ZitadelSMTPProviderStatus defines the observed state of ZitadelSMTPProvider.
type ZitadelSMTPProviderStatus struct {
	// ProviderID in Zitadel.
	// +optional
	ProviderID string `json:"providerId,omitempty"`
	// ConnectionName used when the provider was created (used for cleanup).
	// +optional
	ConnectionName string `json:"connectionName,omitempty"`
	// PasswordHash is a hash of the password last sent to Zitadel.
	// +optional
	PasswordHash string `json:"passwordHash,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=zsmtp,categories=zitadel
// +kubebuilder:printcolumn:name="Host",type=string,JSONPath=`.spec.host`
// +kubebuilder:printcolumn:name="Provider ID",type=string,JSONPath=`.status.providerId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ZitadelSMTPProvider is an SMTP email provider of a Zitadel instance.
type ZitadelSMTPProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZitadelSMTPProviderSpec   `json:"spec,omitempty"`
	Status ZitadelSMTPProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ZitadelSMTPProviderList contains a list of ZitadelSMTPProvider.
type ZitadelSMTPProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ZitadelSMTPProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ZitadelSMTPProvider{}, &ZitadelSMTPProviderList{})
}
