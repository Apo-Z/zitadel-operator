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
	GetMyOrg(ctx context.Context, orgID string) (*Org, error)

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
