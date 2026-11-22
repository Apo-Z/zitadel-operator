package zitadel

import "context"

// Org is a Zitadel organization.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Project is a Zitadel project.
type Project struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	ProjectRoleAssertion bool   `json:"projectRoleAssertion"`
	ProjectRoleCheck     bool   `json:"projectRoleCheck"`
	HasProjectCheck      bool   `json:"hasProjectCheck"`
}

// ProjectSettings are the mutable settings of a project.
type ProjectSettings struct {
	Name                 string `json:"name"`
	ProjectRoleAssertion bool   `json:"projectRoleAssertion"`
	ProjectRoleCheck     bool   `json:"projectRoleCheck"`
	HasProjectCheck      bool   `json:"hasProjectCheck"`
}

// Role is a project role.
type Role struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	Group       string `json:"group"`
}

// OIDCConfig mirrors the Zitadel OIDC application configuration (enum values
// use the Zitadel API names, e.g. OIDC_APP_TYPE_WEB).
type OIDCConfig struct {
	RedirectURIs             []string `json:"redirectUris"`
	ResponseTypes            []string `json:"responseTypes"`
	GrantTypes               []string `json:"grantTypes"`
	AppType                  string   `json:"appType"`
	AuthMethodType           string   `json:"authMethodType"`
	PostLogoutRedirectURIs   []string `json:"postLogoutRedirectUris"`
	Version                  string   `json:"version"`
	DevMode                  bool     `json:"devMode"`
	AccessTokenType          string   `json:"accessTokenType"`
	AccessTokenRoleAssertion bool     `json:"accessTokenRoleAssertion"`
	IDTokenRoleAssertion     bool     `json:"idTokenRoleAssertion"`
	IDTokenUserinfoAssertion bool     `json:"idTokenUserinfoAssertion"`
	AdditionalOrigins        []string `json:"additionalOrigins"`
	ClientID                 string   `json:"clientId,omitempty"`
}

// APIConfig mirrors the Zitadel API application configuration.
type APIConfig struct {
	AuthMethodType string `json:"authMethodType"`
	ClientID       string `json:"clientId,omitempty"`
}

// App is a Zitadel application.
type App struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	OIDCConfig *OIDCConfig `json:"oidcConfig,omitempty"`
	APIConfig  *APIConfig  `json:"apiConfig,omitempty"`
}

// ClientID returns the client ID of the application.
func (a *App) ClientID() string {
	switch {
	case a.OIDCConfig != nil:
		return a.OIDCConfig.ClientID
	case a.APIConfig != nil:
		return a.APIConfig.ClientID
	}
	return ""
}

// AppCredentials are returned when an application is created.
type AppCredentials struct {
	AppID        string `json:"appId"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// AppKey is a JSON key generated for an application (PRIVATE_KEY_JWT).
type AppKey struct {
	ID   string
	JSON []byte
}

// API is the subset of the Zitadel management API used by the operator.
// orgID may be empty to use the organization of the service account.
type API interface {
	InstanceAPI

	GetMyOrg(ctx context.Context, orgID string) (*Org, error)
	GetMyUserOrg(ctx context.Context) (string, error)

	GetProject(ctx context.Context, orgID, projectID string) (*Project, error)
	FindProjectByName(ctx context.Context, orgID, name string) (*Project, error)
	CreateProject(ctx context.Context, orgID string, settings ProjectSettings) (string, error)
	UpdateProject(ctx context.Context, orgID, projectID string, settings ProjectSettings) error
	DeleteProject(ctx context.Context, orgID, projectID string) error

	ListProjectRoles(ctx context.Context, orgID, projectID string) ([]Role, error)
	AddProjectRole(ctx context.Context, orgID, projectID string, role Role) error
	UpdateProjectRole(ctx context.Context, orgID, projectID string, role Role) error
	RemoveProjectRole(ctx context.Context, orgID, projectID, key string) error

	GetApp(ctx context.Context, orgID, projectID, appID string) (*App, error)
	FindAppByName(ctx context.Context, orgID, projectID, name string) (*App, error)
	CreateOIDCApp(ctx context.Context, orgID, projectID, name string, cfg OIDCConfig) (*AppCredentials, error)
	UpdateOIDCApp(ctx context.Context, orgID, projectID, appID string, cfg OIDCConfig) error
	CreateAPIApp(ctx context.Context, orgID, projectID, name string, cfg APIConfig) (*AppCredentials, error)
	UpdateAPIApp(ctx context.Context, orgID, projectID, appID string, cfg APIConfig) error
	RenameApp(ctx context.Context, orgID, projectID, appID, name string) error
	RegenerateClientSecret(ctx context.Context, orgID, projectID, appID string, oidc bool) (string, error)
	AddAppKey(ctx context.Context, orgID, projectID, appID string) (*AppKey, error)
	RemoveAppKey(ctx context.Context, orgID, projectID, appID, keyID string) error
	DeleteApp(ctx context.Context, orgID, projectID, appID string) error
}

// PolicyKind is an organization policy managed as a free-form JSON object.
type PolicyKind string

// Organization policies.
const (
	PolicyPasswordComplexity PolicyKind = "password/complexity"
	PolicyLockout            PolicyKind = "lockout"
	PolicyLogin              PolicyKind = "login"
)

// GoogleIDP is a Google identity provider template.
type GoogleIDP struct {
	Name            string         `json:"name"`
	ClientID        string         `json:"clientId"`
	ClientSecret    string         `json:"clientSecret,omitempty"`
	Scopes          []string       `json:"scopes"`
	ProviderOptions map[string]any `json:"providerOptions"`
}

// IDP is an identity provider of an organization as returned by the API.
type IDP struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Config struct {
		Options map[string]any `json:"options"`
		Google  *struct {
			ClientID string   `json:"clientId"`
			Scopes   []string `json:"scopes"`
		} `json:"google"`
	} `json:"config"`
}

// Action is a Zitadel action.
type Action struct {
	ID            string `json:"id,omitempty"`
	Name          string `json:"name"`
	Script        string `json:"script"`
	Timeout       string `json:"timeout"`
	AllowedToFail bool   `json:"allowedToFail"`
}

// SMTPConfig is the configuration of an SMTP email provider.
type SMTPConfig struct {
	Description    string `json:"description"`
	Host           string `json:"host"`
	User           string `json:"user"`
	Password       string `json:"password,omitempty"`
	TLS            bool   `json:"tls"`
	SenderAddress  string `json:"senderAddress"`
	SenderName     string `json:"senderName"`
	ReplyToAddress string `json:"replyToAddress"`
}

// EmailProvider is an email provider of the instance.
type EmailProvider struct {
	ID          string      `json:"id"`
	State       string      `json:"state"`
	Description string      `json:"description"`
	SMTP        *SMTPConfig `json:"smtp"`
}

// EmailProviderActive is the state of the active email provider.
const EmailProviderActive = "EMAIL_PROVIDER_ACTIVE"

// InstanceAPI is the part of the API used to configure an instance (organizations,
// policies, identity providers, actions, email). It needs instance permissions.
type InstanceAPI interface {
	ListOrganizations(ctx context.Context) ([]Org, error)
	CreateOrganization(ctx context.Context, name string) (string, error)
	DeleteOrganization(ctx context.Context, orgID string) error
	GetDefaultOrganization(ctx context.Context) (string, error)
	SetDefaultOrganization(ctx context.Context, orgID string) error

	// GetPolicy returns the policy of the organization and whether it is still
	// the instance default (no custom policy yet).
	GetPolicy(ctx context.Context, orgID string, kind PolicyKind) (map[string]any, bool, error)
	// SetPolicy creates (create=true) or updates the custom policy of the organization.
	SetPolicy(ctx context.Context, orgID string, kind PolicyKind, policy map[string]any, create bool) error

	ListIDPs(ctx context.Context, orgID string) ([]IDP, error)
	AddGoogleIDP(ctx context.Context, orgID string, idp GoogleIDP) (string, error)
	UpdateGoogleIDP(ctx context.Context, orgID, id string, idp GoogleIDP) error
	DeleteIDP(ctx context.Context, orgID, id string) error

	ListActions(ctx context.Context, orgID string) ([]Action, error)
	CreateAction(ctx context.Context, orgID string, action Action) (string, error)
	UpdateAction(ctx context.Context, orgID, id string, action Action) error
	DeleteAction(ctx context.Context, orgID, id string) error
	// GetFlowTriggers returns the action IDs of each trigger type of a flow.
	GetFlowTriggers(ctx context.Context, orgID, flowType string) (map[string][]string, error)
	SetTriggerActions(ctx context.Context, orgID, flowType, triggerType string, actionIDs []string) error

	ListEmailProviders(ctx context.Context) ([]EmailProvider, error)
	AddSMTPProvider(ctx context.Context, cfg SMTPConfig) (string, error)
	UpdateSMTPProvider(ctx context.Context, id string, cfg SMTPConfig) error
	ActivateEmailProvider(ctx context.Context, id string) error
	DeleteEmailProvider(ctx context.Context, id string) error
}
