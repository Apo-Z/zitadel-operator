package zitadel

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

var policyPaths = map[PolicyKind]string{
	PolicyPasswordComplexity: "/management/v1/policies/password/complexity",
	PolicyLockout:            "/management/v1/policies/lockout",
	PolicyLogin:              "/management/v1/policies/login",
}

var listAll = map[string]any{"query": map[string]any{"limit": 1000}}

// ListOrganizations returns every organization of the instance.
func (c *Client) ListOrganizations(ctx context.Context) ([]Org, error) {
	var out struct {
		Result []Org `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/v2/organizations/_search", "", listAll, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

// CreateOrganization creates an organization and returns its ID.
func (c *Client) CreateOrganization(ctx context.Context, name string) (string, error) {
	var out struct {
		OrganizationID string `json:"organizationId"`
	}
	if err := c.do(ctx, http.MethodPost, "/v2/organizations", "", map[string]any{"name": name}, &out); err != nil {
		return "", err
	}
	return out.OrganizationID, nil
}

// DeleteOrganization deletes an organization.
func (c *Client) DeleteOrganization(ctx context.Context, orgID string) error {
	return c.do(ctx, http.MethodDelete, p("/v2/organizations/%s", orgID), "", nil, nil)
}

// GetDefaultOrganization returns the ID of the default organization.
func (c *Client) GetDefaultOrganization(ctx context.Context) (string, error) {
	var out struct {
		Org Org `json:"org"`
	}
	if err := c.do(ctx, http.MethodGet, "/admin/v1/orgs/default", "", nil, &out); err != nil {
		return "", err
	}
	return out.Org.ID, nil
}

// SetDefaultOrganization sets the default organization of the instance.
func (c *Client) SetDefaultOrganization(ctx context.Context, orgID string) error {
	return ignoreNoChanges(c.do(ctx, http.MethodPut, p("/admin/v1/orgs/default/%s", orgID), "", map[string]any{}, nil))
}

// GetPolicy implements InstanceAPI.
func (c *Client) GetPolicy(ctx context.Context, orgID string, kind PolicyKind) (map[string]any, bool, error) {
	var out struct {
		Policy map[string]any `json:"policy"`
	}
	if err := c.do(ctx, http.MethodGet, policyPaths[kind], orgID, nil, &out); err != nil {
		return nil, false, err
	}
	isDefault, _ := out.Policy["isDefault"].(bool)
	return out.Policy, isDefault, nil
}

// SetPolicy implements InstanceAPI.
func (c *Client) SetPolicy(ctx context.Context, orgID string, kind PolicyKind, policy map[string]any, create bool) error {
	method := http.MethodPut
	if create {
		method = http.MethodPost
	}
	err := c.do(ctx, method, policyPaths[kind], orgID, policy, nil)
	var e *Error
	if errors.As(err, &e) && strings.Contains(strings.ToLower(e.Message), "notchanged") {
		return nil
	}
	return ignoreNoChanges(err)
}

// ListIDPs returns the identity providers of an organization.
func (c *Client) ListIDPs(ctx context.Context, orgID string) ([]IDP, error) {
	var out struct {
		Result []IDP `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/management/v1/idps/templates/_search", orgID, listAll, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

// AddGoogleIDP adds a Google identity provider and returns its ID.
func (c *Client) AddGoogleIDP(ctx context.Context, orgID string, idp GoogleIDP) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/management/v1/idps/google", orgID, idp, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// UpdateGoogleIDP updates a Google identity provider (the secret is kept when empty).
func (c *Client) UpdateGoogleIDP(ctx context.Context, orgID, id string, idp GoogleIDP) error {
	return ignoreNoChanges(c.do(ctx, http.MethodPut, p("/management/v1/idps/google/%s", id), orgID, idp, nil))
}

// DeleteIDP deletes an identity provider (template API: /management/v1/idps/{id}
// only knows the legacy IdP configurations and answers "not found").
func (c *Client) DeleteIDP(ctx context.Context, orgID, id string) error {
	return c.do(ctx, http.MethodDelete, p("/management/v1/idps/templates/%s", id), orgID, nil, nil)
}

// ListActions returns the actions of an organization.
func (c *Client) ListActions(ctx context.Context, orgID string) ([]Action, error) {
	var out struct {
		Result []Action `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/management/v1/actions/_search", orgID, listAll, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

// CreateAction creates an action and returns its ID.
func (c *Client) CreateAction(ctx context.Context, orgID string, action Action) (string, error) {
	action.ID = ""
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/management/v1/actions", orgID, action, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// UpdateAction updates an action.
func (c *Client) UpdateAction(ctx context.Context, orgID, id string, action Action) error {
	action.ID = ""
	return ignoreNoChanges(c.do(ctx, http.MethodPut, p("/management/v1/actions/%s", id), orgID, action, nil))
}

// DeleteAction deletes an action.
func (c *Client) DeleteAction(ctx context.Context, orgID, id string) error {
	return c.do(ctx, http.MethodDelete, p("/management/v1/actions/%s", id), orgID, nil, nil)
}

// GetFlowTriggers implements InstanceAPI.
func (c *Client) GetFlowTriggers(ctx context.Context, orgID, flowType string) (map[string][]string, error) {
	var out struct {
		Flow struct {
			TriggerActions []struct {
				TriggerType struct {
					ID string `json:"id"`
				} `json:"triggerType"`
				Actions []struct {
					ID string `json:"id"`
				} `json:"actions"`
			} `json:"triggerActions"`
		} `json:"flow"`
	}
	if err := c.do(ctx, http.MethodGet, p("/management/v1/flows/%s", flowType), orgID, nil, &out); err != nil {
		return nil, err
	}
	triggers := map[string][]string{}
	for _, t := range out.Flow.TriggerActions {
		for _, a := range t.Actions {
			triggers[t.TriggerType.ID] = append(triggers[t.TriggerType.ID], a.ID)
		}
	}
	return triggers, nil
}

// SetTriggerActions sets the actions of a trigger of a flow.
func (c *Client) SetTriggerActions(ctx context.Context, orgID, flowType, triggerType string, actionIDs []string) error {
	body := map[string]any{"actionIds": actionIDs}
	return ignoreNoChanges(c.do(ctx, http.MethodPost,
		p("/management/v1/flows/%s/trigger/%s", flowType, triggerType), orgID, body, nil))
}

// ListEmailProviders returns the email providers of the instance.
func (c *Client) ListEmailProviders(ctx context.Context) ([]EmailProvider, error) {
	var out struct {
		Result []EmailProvider `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/admin/v1/email/_search", "", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

// AddSMTPProvider adds an SMTP provider and returns its ID.
func (c *Client) AddSMTPProvider(ctx context.Context, cfg SMTPConfig) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/admin/v1/email/smtp", "", cfg, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// UpdateSMTPProvider updates an SMTP provider (the password is kept when empty).
func (c *Client) UpdateSMTPProvider(ctx context.Context, id string, cfg SMTPConfig) error {
	return ignoreNoChanges(c.do(ctx, http.MethodPut, p("/admin/v1/email/smtp/%s", id), "", cfg, nil))
}

// ActivateEmailProvider makes an email provider the active one.
func (c *Client) ActivateEmailProvider(ctx context.Context, id string) error {
	return ignoreNoChanges(c.do(ctx, http.MethodPost, p("/admin/v1/email/%s/_activate", id), "", map[string]any{}, nil))
}

// DeleteEmailProvider deletes an email provider.
func (c *Client) DeleteEmailProvider(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, p("/admin/v1/email/%s", id), "", nil, nil)
}
