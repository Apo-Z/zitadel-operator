package controller

import (
	"context"
	"maps"
	"slices"

	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

// In-memory implementation of zitadel.InstanceAPI for fakeZitadel.

type fakeInstance struct {
	orgs       map[string]string // id -> name
	defaultOrg string
	policies   map[string]map[string]any // org/kind -> custom policy
	idps       map[string]map[string]*fakeIDP
	actions    map[string]map[string]*zitadel.Action
	flows      map[string]map[string][]string // org/flow -> trigger -> action IDs
	email      map[string]*zitadel.EmailProvider
	emailPwd   map[string]string
}

type fakeIDP struct {
	idp    zitadel.GoogleIDP
	id     string
	secret string
}

func (f *fakeZitadel) inst() *fakeInstance {
	if f.instance == nil {
		f.instance = &fakeInstance{orgs: map[string]string{"org-1": "ZITADEL"}, defaultOrg: "org-1",
			policies: map[string]map[string]any{}, idps: map[string]map[string]*fakeIDP{},
			actions: map[string]map[string]*zitadel.Action{}, flows: map[string]map[string][]string{},
			email: map[string]*zitadel.EmailProvider{}, emailPwd: map[string]string{}}
	}
	return f.instance
}

func (f *fakeZitadel) ListOrganizations(context.Context) ([]zitadel.Org, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []zitadel.Org
	for id, name := range f.inst().orgs {
		out = append(out, zitadel.Org{ID: id, Name: name})
	}
	return out, nil
}

func (f *fakeZitadel) CreateOrganization(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("CreateOrganization")
	id := f.id("org-")
	f.inst().orgs[id] = name
	return id, nil
}

func (f *fakeZitadel) DeleteOrganization(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.inst().orgs, id)
	return nil
}

func (f *fakeZitadel) GetDefaultOrganization(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inst().defaultOrg, nil
}

func (f *fakeZitadel) SetDefaultOrganization(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("SetDefaultOrganization")
	f.inst().defaultOrg = id
	return nil
}

func (f *fakeZitadel) GetPolicy(_ context.Context, orgID string, kind zitadel.PolicyKind) (map[string]any, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.inst().policies[orgID+"/"+string(kind)]
	if !ok {
		// Instance default: Zitadel returns int64 as strings.
		return map[string]any{"minLength": "8", "isDefault": true}, true, nil
	}
	out := maps.Clone(p)
	return out, false, nil
}

func (f *fakeZitadel) SetPolicy(_ context.Context, orgID string, kind zitadel.PolicyKind, policy map[string]any, create bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := orgID + "/" + string(kind)
	if _, exists := f.inst().policies[key]; exists == create {
		return &zitadel.Error{HTTPStatus: 400, Code: 9, Message: "wrong method"}
	}
	f.track("SetPolicy")
	f.inst().policies[key] = maps.Clone(policy)
	return nil
}

func (f *fakeZitadel) ListIDPs(_ context.Context, orgID string) ([]zitadel.IDP, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []zitadel.IDP
	for _, i := range f.inst().idps[orgID] {
		var idp zitadel.IDP
		idp.ID, idp.Name = i.id, i.idp.Name
		idp.Config.Options = maps.Clone(i.idp.ProviderOptions)
		idp.Config.Google = &struct {
			ClientID string   `json:"clientId"`
			Scopes   []string `json:"scopes"`
		}{ClientID: i.idp.ClientID, Scopes: slices.Clone(i.idp.Scopes)}
		out = append(out, idp)
	}
	return out, nil
}

func (f *fakeZitadel) AddGoogleIDP(_ context.Context, orgID string, idp zitadel.GoogleIDP) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("AddGoogleIDP")
	if f.inst().idps[orgID] == nil {
		f.inst().idps[orgID] = map[string]*fakeIDP{}
	}
	id := f.id("idp-")
	f.inst().idps[orgID][id] = &fakeIDP{idp: idp, id: id, secret: idp.ClientSecret}
	return id, nil
}

func (f *fakeZitadel) UpdateGoogleIDP(_ context.Context, orgID, id string, idp zitadel.GoogleIDP) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("UpdateGoogleIDP")
	cur, ok := f.inst().idps[orgID][id]
	if !ok {
		return notFound()
	}
	if idp.ClientSecret != "" {
		cur.secret = idp.ClientSecret
	}
	cur.idp = idp
	return nil
}

func (f *fakeZitadel) DeleteIDP(_ context.Context, orgID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("DeleteIDP")
	delete(f.inst().idps[orgID], id)
	return nil
}

func (f *fakeZitadel) ListActions(_ context.Context, orgID string) ([]zitadel.Action, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []zitadel.Action
	for _, a := range f.inst().actions[orgID] {
		out = append(out, *a)
	}
	return out, nil
}

func (f *fakeZitadel) CreateAction(_ context.Context, orgID string, a zitadel.Action) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("CreateAction")
	if f.inst().actions[orgID] == nil {
		f.inst().actions[orgID] = map[string]*zitadel.Action{}
	}
	a.ID = f.id("action-")
	f.inst().actions[orgID][a.ID] = &a
	return a.ID, nil
}

func (f *fakeZitadel) UpdateAction(_ context.Context, orgID, id string, a zitadel.Action) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("UpdateAction")
	a.ID = id
	f.inst().actions[orgID][id] = &a
	return nil
}

func (f *fakeZitadel) DeleteAction(_ context.Context, orgID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("DeleteAction")
	delete(f.inst().actions[orgID], id)
	// Zitadel removes a deleted action from the flows.
	for key, triggers := range f.inst().flows {
		for t, ids := range triggers {
			f.inst().flows[key][t] = slices.DeleteFunc(ids, func(x string) bool { return x == id })
		}
	}
	return nil
}

func (f *fakeZitadel) GetFlowTriggers(_ context.Context, orgID, flow string) (map[string][]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string][]string{}
	for t, ids := range f.inst().flows[orgID+"/"+flow] {
		if len(ids) > 0 {
			out[t] = slices.Clone(ids)
		}
	}
	return out, nil
}

func (f *fakeZitadel) SetTriggerActions(_ context.Context, orgID, flow, trigger string, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("SetTriggerActions")
	key := orgID + "/" + flow
	if f.inst().flows[key] == nil {
		f.inst().flows[key] = map[string][]string{}
	}
	f.inst().flows[key][trigger] = slices.Clone(ids)
	return nil
}

func (f *fakeZitadel) ListEmailProviders(context.Context) ([]zitadel.EmailProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []zitadel.EmailProvider
	for _, p := range f.inst().email {
		cp := *p
		smtp := *p.SMTP
		cp.SMTP = &smtp
		out = append(out, cp)
	}
	return out, nil
}

func (f *fakeZitadel) AddSMTPProvider(_ context.Context, cfg zitadel.SMTPConfig) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("AddSMTPProvider")
	id := f.id("smtp-")
	f.inst().emailPwd[id] = cfg.Password
	cfg.Password = ""
	f.inst().email[id] = &zitadel.EmailProvider{ID: id, State: "EMAIL_PROVIDER_INACTIVE", Description: cfg.Description, SMTP: &cfg}
	return id, nil
}

func (f *fakeZitadel) UpdateSMTPProvider(_ context.Context, id string, cfg zitadel.SMTPConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("UpdateSMTPProvider")
	p, ok := f.inst().email[id]
	if !ok {
		return notFound()
	}
	if cfg.Password != "" {
		f.inst().emailPwd[id] = cfg.Password
	}
	cfg.Password = ""
	p.Description, p.SMTP = cfg.Description, &cfg
	return nil
}

func (f *fakeZitadel) ActivateEmailProvider(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("ActivateEmailProvider")
	for _, p := range f.inst().email {
		p.State = "EMAIL_PROVIDER_INACTIVE"
	}
	f.inst().email[id].State = zitadel.EmailProviderActive
	return nil
}

func (f *fakeZitadel) DeleteEmailProvider(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.inst().email, id)
	return nil
}
