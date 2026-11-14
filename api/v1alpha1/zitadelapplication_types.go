package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ApplicationType is the kind of Zitadel application.
// +kubebuilder:validation:Enum=OIDC;API
type ApplicationType string

const (
	ApplicationTypeOIDC ApplicationType = "OIDC"
	ApplicationTypeAPI  ApplicationType = "API"
)

// OIDCAppType is the OIDC client profile.
// +kubebuilder:validation:Enum=WEB;USER_AGENT;NATIVE
type OIDCAppType string

// AuthMethod is the client authentication method.
// BASIC/POST produce a client secret, PRIVATE_KEY_JWT produces a JSON key,
// NONE is for public clients (PKCE).
// +kubebuilder:validation:Enum=BASIC;POST;NONE;PRIVATE_KEY_JWT
type AuthMethod string

const (
	AuthMethodBasic         AuthMethod = "BASIC"
	AuthMethodPost          AuthMethod = "POST"
	AuthMethodNone          AuthMethod = "NONE"
	AuthMethodPrivateKeyJWT AuthMethod = "PRIVATE_KEY_JWT"
)

// OIDCGrantType is an OAuth2 grant type.
// +kubebuilder:validation:Enum=AUTHORIZATION_CODE;IMPLICIT;REFRESH_TOKEN;DEVICE_CODE;TOKEN_EXCHANGE
type OIDCGrantType string

// OIDCResponseType is an OAuth2 response type.
// +kubebuilder:validation:Enum=CODE;ID_TOKEN;ID_TOKEN_TOKEN
type OIDCResponseType string

// OIDCTokenType is the access token format.
// +kubebuilder:validation:Enum=BEARER;JWT
type OIDCTokenType string

// OIDCConfig configures an OIDC application.
type OIDCConfig struct {
	// AppType of the client.
	// +kubebuilder:default=WEB
	// +optional
	AppType OIDCAppType `json:"appType,omitempty"`
	// AuthMethod of the client.
	// +kubebuilder:default=BASIC
	// +optional
	AuthMethod AuthMethod `json:"authMethod,omitempty"`
	// RedirectURIs allowed after login.
	// +optional
	RedirectURIs []string `json:"redirectUris,omitempty"`
	// PostLogoutRedirectURIs allowed after logout.
	// +optional
	PostLogoutRedirectURIs []string `json:"postLogoutRedirectUris,omitempty"`
	// GrantTypes. Defaults to [AUTHORIZATION_CODE].
	// +optional
	GrantTypes []OIDCGrantType `json:"grantTypes,omitempty"`
	// ResponseTypes. Defaults to [CODE].
	// +optional
	ResponseTypes []OIDCResponseType `json:"responseTypes,omitempty"`
	// AccessTokenType issued to the client.
	// +kubebuilder:default=BEARER
	// +optional
	AccessTokenType OIDCTokenType `json:"accessTokenType,omitempty"`
	// DevMode allows non-https redirect URIs.
	// +optional
	DevMode bool `json:"devMode,omitempty"`
	// AccessTokenRoleAssertion adds roles to the access token.
	// +optional
	AccessTokenRoleAssertion bool `json:"accessTokenRoleAssertion,omitempty"`
	// IDTokenRoleAssertion adds roles to the ID token.
	// +optional
	IDTokenRoleAssertion bool `json:"idTokenRoleAssertion,omitempty"`
	// IDTokenUserinfoAssertion adds userinfo claims to the ID token.
	// +optional
	IDTokenUserinfoAssertion bool `json:"idTokenUserinfoAssertion,omitempty"`
	// AdditionalOrigins allowed for CORS.
	// +optional
	AdditionalOrigins []string `json:"additionalOrigins,omitempty"`
}

// APIConfig configures an API application (resource server, e.g. for token introspection).
type APIConfig struct {
	// AuthMethod of the API client.
	// +kubebuilder:validation:Enum=BASIC;PRIVATE_KEY_JWT
	// +kubebuilder:default=PRIVATE_KEY_JWT
	// +optional
	AuthMethod AuthMethod `json:"authMethod,omitempty"`
}

// ApplicationSecretSpec configures the Secret generated for the application.
type ApplicationSecretSpec struct {
	// Name of the Secret, created in the namespace of the ZitadelApplication.
	// Defaults to metadata.name of the ZitadelApplication.
	// +optional
	Name string `json:"name,omitempty"`

	// Template maps Secret keys to Go templates. Available fields:
	// .ClientID .ClientSecret .KeyJSON .Issuer .DiscoveryURL .ProjectID .AppID .OrganizationID .AppName.
	// Example: {"OIDC_ISSUER_URL": "{{ .Issuer }}", "config.yaml": "clientId: {{ .ClientID }}"}.
	// When empty the default keys are used: CLIENT_ID, CLIENT_SECRET, ISSUER,
	// DISCOVERY_URL, PROJECT_ID, APP_ID, ORGANIZATION_ID and key.json.
	// +optional
	Template map[string]string `json:"template,omitempty"`

	// Labels added to the Secret.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations added to the Secret (e.g. reloader annotations).
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ZitadelApplicationSpec defines the desired state of ZitadelApplication.
// +kubebuilder:validation:XValidation:rule="has(self.projectRef) != has(self.projectId)",message="exactly one of projectRef or projectId must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.projectId) || has(self.connectionRef)",message="connectionRef is required when projectId is set"
// +kubebuilder:validation:XValidation:rule="self.type != 'API' || !has(self.oidc)",message="oidc must not be set for API applications"
type ZitadelApplicationSpec struct {
	// ProjectRef references a ZitadelProject in the same namespace.
	// +optional
	ProjectRef *LocalObjectReference `json:"projectRef,omitempty"`

	// ProjectID of an existing Zitadel project not managed by this operator.
	// Requires connectionRef.
	// +optional
	ProjectID string `json:"projectId,omitempty"`
	// ConnectionRef to use with projectId.
	// +optional
	ConnectionRef *ConnectionReference `json:"connectionRef,omitempty"`
	// OrganizationID of the project referenced by projectId. Defaults to the connection's organization.
	// +optional
	OrganizationID string `json:"organizationId,omitempty"`

	// Name of the application in Zitadel. Defaults to metadata.name.
	// If an application with this name already exists in the project, it is adopted.
	// +optional
	Name string `json:"name,omitempty"`

	// Type of application.
	// +kubebuilder:default=OIDC
	// +optional
	Type ApplicationType `json:"type,omitempty"`

	// OIDC configuration (type OIDC).
	// +optional
	OIDC *OIDCConfig `json:"oidc,omitempty"`

	// API configuration (type API).
	// +optional
	API *APIConfig `json:"api,omitempty"`

	// Secret configures the generated Secret.
	// +optional
	Secret ApplicationSecretSpec `json:"secret,omitempty"`

	// DeletionPolicy defines what happens in Zitadel when this object is deleted.
	// +kubebuilder:default=Delete
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
}

// ZitadelApplicationStatus defines the observed state of ZitadelApplication.
type ZitadelApplicationStatus struct {
	// AppID in Zitadel.
	// +optional
	AppID string `json:"appId,omitempty"`
	// ClientID of the application.
	// +optional
	ClientID string `json:"clientId,omitempty"`
	// ProjectID the application belongs to.
	// +optional
	ProjectID string `json:"projectId,omitempty"`
	// OrganizationID owning the project.
	// +optional
	OrganizationID string `json:"organizationId,omitempty"`
	// ConnectionName used (kept for cleanup).
	// +optional
	ConnectionName string `json:"connectionName,omitempty"`
	// KeyID of the current application key (PRIVATE_KEY_JWT).
	// +optional
	KeyID string `json:"keyId,omitempty"`
	// SecretName of the generated Secret.
	// +optional
	SecretName string `json:"secretName,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=zapp,categories=zitadel
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Client ID",type=string,JSONPath=`.status.clientId`
// +kubebuilder:printcolumn:name="Secret",type=string,JSONPath=`.status.secretName`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ZitadelApplication is an application (OIDC client or API) in a Zitadel
// project. Its credentials are written to a Secret in the same namespace.
type ZitadelApplication struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZitadelApplicationSpec   `json:"spec,omitempty"`
	Status ZitadelApplicationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ZitadelApplicationList contains a list of ZitadelApplication.
type ZitadelApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ZitadelApplication `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ZitadelApplication{}, &ZitadelApplicationList{})
}
