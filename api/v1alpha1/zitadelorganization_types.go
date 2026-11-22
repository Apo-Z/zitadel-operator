package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// IdentityProviderType is the type of an external identity provider.
// +kubebuilder:validation:Enum=Google
type IdentityProviderType string

const (
	// IdentityProviderTypeGoogle is a Google identity provider.
	IdentityProviderTypeGoogle IdentityProviderType = "Google"
)

// IdentityProviderCredentials locates the client ID and secret of an identity provider.
type IdentityProviderCredentials struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Namespace of the Secret.
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
	// ClientIDKey is the key holding the client ID.
	// +kubebuilder:default=CLIENT_ID
	// +optional
	ClientIDKey string `json:"clientIdKey,omitempty"`
	// ClientSecretKey is the key holding the client secret.
	// +kubebuilder:default=CLIENT_SECRET
	// +optional
	ClientSecretKey string `json:"clientSecretKey,omitempty"`
}

// IdentityProvider is an external identity provider of the organization.
type IdentityProvider struct {
	// Name of the provider in Zitadel (unique in the organization). An existing
	// provider with this name is adopted.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Type of the provider.
	Type IdentityProviderType `json:"type"`
	// CredentialsSecretRef holds the OAuth client ID and secret.
	CredentialsSecretRef IdentityProviderCredentials `json:"credentialsSecretRef"`
	// Scopes requested from the provider.
	// +optional
	Scopes []string `json:"scopes,omitempty"`
	// Options are the Zitadel provider options (isLinkingAllowed, isCreationAllowed,
	// isAutoCreation, isAutoUpdate, autoLinking…), sent as-is.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	// +optional
	Options *runtime.RawExtension `json:"options,omitempty"`
}

// ActionTrigger attaches an action to a trigger of a flow.
type ActionTrigger struct {
	// FlowType is the Zitadel flow type ID, e.g. "2" (complement token).
	// +kubebuilder:validation:Pattern=`^[0-9]+$`
	FlowType string `json:"flowType"`
	// TriggerTypes are the trigger type IDs of the flow, e.g. ["4", "5"]
	// (pre userinfo creation, pre access token creation).
	// +kubebuilder:validation:MinItems=1
	TriggerTypes []string `json:"triggerTypes"`
}

// Action is a Zitadel action (JavaScript) of the organization.
type Action struct {
	// Name of the action; it must match the function name of the script.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Script of the action.
	// +kubebuilder:validation:MinLength=1
	Script string `json:"script"`
	// Timeout of the action, e.g. "10s".
	// +kubebuilder:default="10s"
	// +optional
	Timeout string `json:"timeout,omitempty"`
	// AllowedToFail lets the flow continue when the action fails.
	// +optional
	AllowedToFail bool `json:"allowedToFail,omitempty"`
	// Triggers where the action runs.
	// +optional
	Triggers []ActionTrigger `json:"triggers,omitempty"`
}

// ZitadelOrganizationSpec defines the desired state of ZitadelOrganization.
type ZitadelOrganizationSpec struct {
	// ConnectionRef references the ZitadelConnection to use. Its service account
	// needs instance permissions (IAM_OWNER).
	ConnectionRef ConnectionReference `json:"connectionRef"`

	// Name of the organization. Defaults to metadata.name. An existing
	// organization with this name is adopted.
	// +optional
	Name string `json:"name,omitempty"`

	// Default makes it the default organization of the instance.
	// +optional
	Default bool `json:"default,omitempty"`

	// PasswordComplexityPolicy of the organization (hasLowercase, hasUppercase,
	// hasNumber, hasSymbol, minLength), sent as-is: give every field.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	// +optional
	PasswordComplexityPolicy *runtime.RawExtension `json:"passwordComplexityPolicy,omitempty"`
	// LockoutPolicy of the organization (maxPasswordAttempts, maxOtpAttempts).
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	// +optional
	LockoutPolicy *runtime.RawExtension `json:"lockoutPolicy,omitempty"`
	// LoginPolicy of the organization (allowRegister, allowExternalIdp,
	// secondFactors…), sent as-is: give every field.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	// +optional
	LoginPolicy *runtime.RawExtension `json:"loginPolicy,omitempty"`

	// IdentityProviders of the organization. Providers created by the operator and
	// later removed from this list are deleted; other providers are left untouched.
	// +listType=map
	// +listMapKey=name
	// +optional
	IdentityProviders []IdentityProvider `json:"identityProviders,omitempty"`

	// Actions of the organization. Actions created by the operator and later removed
	// from this list are deleted; other actions are left untouched.
	// +listType=map
	// +listMapKey=name
	// +optional
	Actions []Action `json:"actions,omitempty"`

	// DeletionPolicy defines what happens in Zitadel when this object is deleted.
	// Deleting an organization deletes all its users, projects and applications.
	// +kubebuilder:default=Retain
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
}

// ZitadelOrganizationStatus defines the observed state of ZitadelOrganization.
type ZitadelOrganizationStatus struct {
	// OrganizationID in Zitadel.
	// +optional
	OrganizationID string `json:"organizationId,omitempty"`
	// ConnectionName used when the organization was created (used for cleanup).
	// +optional
	ConnectionName string `json:"connectionName,omitempty"`
	// ManagedIdentityProviders are the provider names created by the operator.
	// +optional
	ManagedIdentityProviders []string `json:"managedIdentityProviders,omitempty"`
	// ManagedActions are the action names created by the operator.
	// +optional
	ManagedActions []string `json:"managedActions,omitempty"`
	// CredentialsHashes are hashes of the identity provider credentials last sent
	// to Zitadel, by provider name (to resend them when the Secret changes).
	// +optional
	CredentialsHashes map[string]string `json:"credentialsHashes,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=zorg,categories=zitadel
// +kubebuilder:printcolumn:name="Organization ID",type=string,JSONPath=`.status.organizationId`
// +kubebuilder:printcolumn:name="Default",type=boolean,JSONPath=`.spec.default`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ZitadelOrganization is an organization of a Zitadel instance with its
// policies, identity providers and actions.
type ZitadelOrganization struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZitadelOrganizationSpec   `json:"spec,omitempty"`
	Status ZitadelOrganizationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ZitadelOrganizationList contains a list of ZitadelOrganization.
type ZitadelOrganizationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ZitadelOrganization `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ZitadelOrganization{}, &ZitadelOrganizationList{})
}
