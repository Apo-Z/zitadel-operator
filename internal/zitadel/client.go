// Package zitadel is a small client for the Zitadel management REST API (v1).
package zitadel

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config configures a Client.
type Config struct {
	// Issuer is the public URL of the instance.
	Issuer string
	// APIURL is the endpoint used for requests (defaults to Issuer).
	APIURL string
	// Credentials is a service account JSON key or a personal access token.
	Credentials []byte
	// InsecureSkipTLSVerify disables TLS verification.
	InsecureSkipTLSVerify bool
	// Timeout of each HTTP request.
	Timeout time.Duration
}

// Client implements API over HTTP.
type Client struct {
	apiURL string
	host   string
	http   *http.Client
	tokens TokenSource
}

var _ API = (*Client)(nil)

// NewClient creates a client.
func NewClient(cfg Config) (*Client, error) {
	issuer, err := url.Parse(strings.TrimSuffix(cfg.Issuer, "/"))
	if err != nil || issuer.Host == "" {
		return nil, fmt.Errorf("invalid issuer URL %q", cfg.Issuer)
	}
	apiURL := strings.TrimSuffix(cfg.APIURL, "/")
	host := ""
	if apiURL == "" {
		apiURL = issuer.String()
	} else {
		// Zitadel resolves the instance from the Host header.
		host = issuer.Host
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.InsecureSkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in
	}
	httpClient := &http.Client{Timeout: cfg.Timeout, Transport: transport}
	tokens, err := NewTokenSource(cfg.Credentials, issuer.String(), apiURL, host, httpClient)
	if err != nil {
		return nil, err
	}
	return &Client{apiURL: apiURL, host: host, http: httpClient, tokens: tokens}, nil
}

// Error is an error returned by the Zitadel API.
type Error struct {
	HTTPStatus int
	Code       int    `json:"code"`
	Message    string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("zitadel API error (HTTP %d, code %d): %s", e.HTTPStatus, e.Code, e.Message)
}

// gRPC status codes used by Zitadel.
const (
	codeNotFound           = 5
	codeAlreadyExists      = 6
	codeFailedPrecondition = 9
)

// IsNotFound reports whether err is a "not found" error.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code == codeNotFound || e.HTTPStatus == http.StatusNotFound)
}

// IsAlreadyExists reports whether err is an "already exists" error.
func IsAlreadyExists(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code == codeAlreadyExists || e.HTTPStatus == http.StatusConflict)
}

// isNoChanges reports whether err means that an update did not change anything.
func isNoChanges(err error) bool {
	var e *Error
	if !errors.As(err, &e) || e.Code != codeFailedPrecondition {
		return false
	}
	msg := strings.ToLower(e.Message)
	return strings.Contains(msg, "no change") || strings.Contains(msg, "nochanges")
}

func ignoreNoChanges(err error) error {
	if isNoChanges(err) {
		return nil
	}
	return err
}

func (c *Client) do(ctx context.Context, method, path, orgID string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL+path, body)
	if err != nil {
		return err
	}
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("authenticating to zitadel: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if orgID != "" {
		req.Header.Set("x-zitadel-orgid", orgID)
	}
	if c.host != "" {
		req.Host = c.host
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		apiErr := &Error{HTTPStatus: resp.StatusCode}
		if json.Unmarshal(data, apiErr) != nil || apiErr.Message == "" {
			apiErr.Message = strings.TrimSpace(string(data))
		}
		return apiErr
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decoding response of %s %s: %w", method, path, err)
		}
	}
	return nil
}

func p(format string, args ...string) string {
	escaped := make([]any, len(args))
	for i, a := range args {
		escaped[i] = url.PathEscape(a)
	}
	return fmt.Sprintf(format, escaped...)
}

func nameQuery(name string) map[string]any {
	return map[string]any{
		"query": map[string]any{"limit": 10},
		"queries": []any{map[string]any{
			"nameQuery": map[string]any{"name": name, "method": "TEXT_QUERY_METHOD_EQUALS"},
		}},
	}
}

// GetMyOrg returns the organization in the context of the request.
func (c *Client) GetMyOrg(ctx context.Context, orgID string) (*Org, error) {
	var out struct {
		Org Org `json:"org"`
	}
	if err := c.do(ctx, http.MethodGet, "/management/v1/orgs/me", orgID, nil, &out); err != nil {
		return nil, err
	}
	return &out.Org, nil
}

// GetProject returns a project by ID.
func (c *Client) GetProject(ctx context.Context, orgID, projectID string) (*Project, error) {
	var out struct {
		Project Project `json:"project"`
	}
	if err := c.do(ctx, http.MethodGet, p("/management/v1/projects/%s", projectID), orgID, nil, &out); err != nil {
		return nil, err
	}
	return &out.Project, nil
}

// FindProjectByName returns the project with the exact given name, or nil.
func (c *Client) FindProjectByName(ctx context.Context, orgID, name string) (*Project, error) {
	var out struct {
		Result []Project `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/management/v1/projects/_search", orgID, nameQuery(name), &out); err != nil {
		return nil, err
	}
	for i := range out.Result {
		if out.Result[i].Name == name {
			return &out.Result[i], nil
		}
	}
	return nil, nil
}

// CreateProject creates a project and returns its ID.
func (c *Client) CreateProject(ctx context.Context, orgID string, settings ProjectSettings) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/management/v1/projects", orgID, settings, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// UpdateProject updates a project.
func (c *Client) UpdateProject(ctx context.Context, orgID, projectID string, settings ProjectSettings) error {
	return ignoreNoChanges(c.do(ctx, http.MethodPut, p("/management/v1/projects/%s", projectID), orgID, settings, nil))
}

// DeleteProject deletes a project and all its applications.
func (c *Client) DeleteProject(ctx context.Context, orgID, projectID string) error {
	return c.do(ctx, http.MethodDelete, p("/management/v1/projects/%s", projectID), orgID, nil, nil)
}

// ListProjectRoles lists the roles of a project.
func (c *Client) ListProjectRoles(ctx context.Context, orgID, projectID string) ([]Role, error) {
	var out struct {
		Result []Role `json:"result"`
	}
	in := map[string]any{"query": map[string]any{"limit": 1000}}
	if err := c.do(ctx, http.MethodPost, p("/management/v1/projects/%s/roles/_search", projectID), orgID, in, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

// AddProjectRole adds a role to a project.
func (c *Client) AddProjectRole(ctx context.Context, orgID, projectID string, role Role) error {
	in := map[string]any{"roleKey": role.Key, "displayName": role.DisplayName, "group": role.Group}
	return c.do(ctx, http.MethodPost, p("/management/v1/projects/%s/roles", projectID), orgID, in, nil)
}

// UpdateProjectRole updates the display name and group of a role.
func (c *Client) UpdateProjectRole(ctx context.Context, orgID, projectID string, role Role) error {
	in := map[string]any{"displayName": role.DisplayName, "group": role.Group}
	return ignoreNoChanges(c.do(ctx, http.MethodPut, p("/management/v1/projects/%s/roles/%s", projectID, role.Key), orgID, in, nil))
}

// RemoveProjectRole removes a role from a project.
func (c *Client) RemoveProjectRole(ctx context.Context, orgID, projectID, key string) error {
	return c.do(ctx, http.MethodDelete, p("/management/v1/projects/%s/roles/%s", projectID, key), orgID, nil, nil)
}

// GetApp returns an application.
func (c *Client) GetApp(ctx context.Context, orgID, projectID, appID string) (*App, error) {
	var out struct {
		App App `json:"app"`
	}
	if err := c.do(ctx, http.MethodGet, p("/management/v1/projects/%s/apps/%s", projectID, appID), orgID, nil, &out); err != nil {
		return nil, err
	}
	return &out.App, nil
}

// FindAppByName returns the application with the exact given name, or nil.
func (c *Client) FindAppByName(ctx context.Context, orgID, projectID, name string) (*App, error) {
	var out struct {
		Result []App `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, p("/management/v1/projects/%s/apps/_search", projectID), orgID, nameQuery(name), &out); err != nil {
		return nil, err
	}
	for i := range out.Result {
		if out.Result[i].Name == name {
			return &out.Result[i], nil
		}
	}
	return nil, nil
}

// CreateOIDCApp creates an OIDC application.
func (c *Client) CreateOIDCApp(ctx context.Context, orgID, projectID, name string, cfg OIDCConfig) (*AppCredentials, error) {
	in := struct {
		Name string `json:"name"`
		OIDCConfig
	}{Name: name, OIDCConfig: cfg}
	var out AppCredentials
	if err := c.do(ctx, http.MethodPost, p("/management/v1/projects/%s/apps/oidc", projectID), orgID, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateOIDCApp updates the OIDC configuration of an application.
func (c *Client) UpdateOIDCApp(ctx context.Context, orgID, projectID, appID string, cfg OIDCConfig) error {
	cfg.ClientID = ""
	return ignoreNoChanges(c.do(ctx, http.MethodPut, p("/management/v1/projects/%s/apps/%s/oidc_config", projectID, appID), orgID, cfg, nil))
}

// CreateAPIApp creates an API application.
func (c *Client) CreateAPIApp(ctx context.Context, orgID, projectID, name string, cfg APIConfig) (*AppCredentials, error) {
	in := map[string]any{"name": name, "authMethodType": cfg.AuthMethodType}
	var out AppCredentials
	if err := c.do(ctx, http.MethodPost, p("/management/v1/projects/%s/apps/api", projectID), orgID, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAPIApp updates the API configuration of an application.
func (c *Client) UpdateAPIApp(ctx context.Context, orgID, projectID, appID string, cfg APIConfig) error {
	in := map[string]any{"authMethodType": cfg.AuthMethodType}
	return ignoreNoChanges(c.do(ctx, http.MethodPut, p("/management/v1/projects/%s/apps/%s/api_config", projectID, appID), orgID, in, nil))
}

// RenameApp changes the name of an application.
func (c *Client) RenameApp(ctx context.Context, orgID, projectID, appID, name string) error {
	in := map[string]any{"name": name}
	return ignoreNoChanges(c.do(ctx, http.MethodPut, p("/management/v1/projects/%s/apps/%s", projectID, appID), orgID, in, nil))
}

// RegenerateClientSecret generates a new client secret for an OIDC or API application.
func (c *Client) RegenerateClientSecret(ctx context.Context, orgID, projectID, appID string, oidc bool) (string, error) {
	kind := "api_config"
	if oidc {
		kind = "oidc_config"
	}
	var out struct {
		ClientSecret string `json:"clientSecret"`
	}
	path := p("/management/v1/projects/%s/apps/%s/", projectID, appID) + kind + "/_generate_client_secret"
	if err := c.do(ctx, http.MethodPost, path, orgID, map[string]any{}, &out); err != nil {
		return "", err
	}
	return out.ClientSecret, nil
}

// AddAppKey generates a JSON key for an application (PRIVATE_KEY_JWT).
func (c *Client) AddAppKey(ctx context.Context, orgID, projectID, appID string) (*AppKey, error) {
	var out struct {
		ID         string `json:"id"`
		KeyDetails []byte `json:"keyDetails"`
	}
	in := map[string]any{"type": "KEY_TYPE_JSON"}
	if err := c.do(ctx, http.MethodPost, p("/management/v1/projects/%s/apps/%s/keys", projectID, appID), orgID, in, &out); err != nil {
		return nil, err
	}
	return &AppKey{ID: out.ID, JSON: out.KeyDetails}, nil
}

// RemoveAppKey deletes an application key.
func (c *Client) RemoveAppKey(ctx context.Context, orgID, projectID, appID, keyID string) error {
	return c.do(ctx, http.MethodDelete, p("/management/v1/projects/%s/apps/%s/keys/%s", projectID, appID, keyID), orgID, nil, nil)
}

// DeleteApp deletes an application.
func (c *Client) DeleteApp(ctx context.Context, orgID, projectID, appID string) error {
	return c.do(ctx, http.MethodDelete, p("/management/v1/projects/%s/apps/%s", projectID, appID), orgID, nil, nil)
}
