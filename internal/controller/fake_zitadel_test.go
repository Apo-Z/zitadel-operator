package controller

import (
	"context"
	"fmt"
	"sync"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

// fakeZitadel is an in-memory implementation of zitadel.API.
type fakeZitadel struct {
	mu       sync.Mutex
	seq      int
	projects map[string]*fakeProject
	calls    map[string]int
}

type fakeProject struct {
	org     string
	project zitadel.Project
	roles   map[string]zitadel.Role
	apps    map[string]*fakeApp
}

type fakeApp struct {
	app    zitadel.App
	secret string
	keys   map[string]bool
}

func newFakeZitadel() *fakeZitadel {
	return &fakeZitadel{projects: map[string]*fakeProject{}, calls: map[string]int{}}
}

func (f *fakeZitadel) ForConnection(context.Context, *zv1.ZitadelConnection) (zitadel.API, error) {
	return f, nil
}

func notFound() error { return &zitadel.Error{HTTPStatus: 404, Code: 5, Message: "not found"} }

func (f *fakeZitadel) id(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s%d", prefix, f.seq)
}

func (f *fakeZitadel) track(name string) {
	f.calls[name]++
}

func (f *fakeZitadel) project(orgID, id string) (*fakeProject, error) {
	p, ok := f.projects[id]
	if !ok || p.org != orgID {
		return nil, notFound()
	}
	return p, nil
}

func (f *fakeZitadel) app(orgID, projectID, appID string) (*fakeApp, error) {
	p, err := f.project(orgID, projectID)
	if err != nil {
		return nil, err
	}
	a, ok := p.apps[appID]
	if !ok {
		return nil, notFound()
	}
	return a, nil
}

func (f *fakeZitadel) GetMyOrg(_ context.Context, orgID string) (*zitadel.Org, error) {
	if orgID == "" {
		orgID = "org-1"
	}
	return &zitadel.Org{ID: orgID, Name: "ACME"}, nil
}

func (f *fakeZitadel) GetProject(_ context.Context, orgID, id string) (*zitadel.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.project(orgID, id)
	if err != nil {
		return nil, err
	}
	cp := p.project
	return &cp, nil
}

func (f *fakeZitadel) FindProjectByName(_ context.Context, orgID, name string) (*zitadel.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.projects {
		if p.org == orgID && p.project.Name == name {
			cp := p.project
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeZitadel) CreateProject(_ context.Context, orgID string, s zitadel.ProjectSettings) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("CreateProject")
	id := f.id("proj-")
	f.projects[id] = &fakeProject{org: orgID, project: zitadel.Project{ID: id, Name: s.Name,
		ProjectRoleAssertion: s.ProjectRoleAssertion, ProjectRoleCheck: s.ProjectRoleCheck, HasProjectCheck: s.HasProjectCheck},
		roles: map[string]zitadel.Role{}, apps: map[string]*fakeApp{}}
	return id, nil
}

func (f *fakeZitadel) UpdateProject(_ context.Context, orgID, id string, s zitadel.ProjectSettings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("UpdateProject")
	p, err := f.project(orgID, id)
	if err != nil {
		return err
	}
	p.project = zitadel.Project{ID: id, Name: s.Name, ProjectRoleAssertion: s.ProjectRoleAssertion,
		ProjectRoleCheck: s.ProjectRoleCheck, HasProjectCheck: s.HasProjectCheck}
	return nil
}

func (f *fakeZitadel) DeleteProject(_ context.Context, orgID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.project(orgID, id); err != nil {
		return err
	}
	delete(f.projects, id)
	return nil
}

func (f *fakeZitadel) ListProjectRoles(_ context.Context, orgID, id string) ([]zitadel.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.project(orgID, id)
	if err != nil {
		return nil, err
	}
	var out []zitadel.Role
	for _, r := range p.roles {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeZitadel) AddProjectRole(_ context.Context, orgID, id string, role zitadel.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.project(orgID, id)
	if err != nil {
		return err
	}
	p.roles[role.Key] = role
	return nil
}

func (f *fakeZitadel) UpdateProjectRole(ctx context.Context, orgID, id string, role zitadel.Role) error {
	return f.AddProjectRole(ctx, orgID, id, role)
}

func (f *fakeZitadel) RemoveProjectRole(_ context.Context, orgID, id, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.project(orgID, id)
	if err != nil {
		return err
	}
	delete(p.roles, key)
	return nil
}

func (f *fakeZitadel) GetApp(_ context.Context, orgID, projectID, appID string) (*zitadel.App, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, err := f.app(orgID, projectID, appID)
	if err != nil {
		return nil, err
	}
	cp := a.app
	return &cp, nil
}

func (f *fakeZitadel) FindAppByName(_ context.Context, orgID, projectID, name string) (*zitadel.App, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.project(orgID, projectID)
	if err != nil {
		return nil, err
	}
	for _, a := range p.apps {
		if a.app.Name == name {
			cp := a.app
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeZitadel) createApp(orgID, projectID, name string, oidc *zitadel.OIDCConfig, api *zitadel.APIConfig) (*zitadel.AppCredentials, error) {
	p, err := f.project(orgID, projectID)
	if err != nil {
		return nil, err
	}
	id := f.id("app-")
	clientID := id + "@" + projectID
	a := &fakeApp{app: zitadel.App{ID: id, Name: name}, secret: f.id("secret-"), keys: map[string]bool{}}
	if oidc != nil {
		cfg := *oidc
		cfg.ClientID = clientID
		a.app.OIDCConfig = &cfg
		if cfg.AuthMethodType == "OIDC_AUTH_METHOD_TYPE_NONE" || cfg.AuthMethodType == "OIDC_AUTH_METHOD_TYPE_PRIVATE_KEY_JWT" {
			a.secret = ""
		}
	} else {
		cfg := *api
		cfg.ClientID = clientID
		a.app.APIConfig = &cfg
		if cfg.AuthMethodType == "API_AUTH_METHOD_TYPE_PRIVATE_KEY_JWT" {
			a.secret = ""
		}
	}
	p.apps[id] = a
	return &zitadel.AppCredentials{AppID: id, ClientID: clientID, ClientSecret: a.secret}, nil
}

func (f *fakeZitadel) CreateOIDCApp(_ context.Context, orgID, projectID, name string, cfg zitadel.OIDCConfig) (*zitadel.AppCredentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("CreateApp")
	return f.createApp(orgID, projectID, name, &cfg, nil)
}

func (f *fakeZitadel) CreateAPIApp(_ context.Context, orgID, projectID, name string, cfg zitadel.APIConfig) (*zitadel.AppCredentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("CreateApp")
	return f.createApp(orgID, projectID, name, nil, &cfg)
}

func (f *fakeZitadel) UpdateOIDCApp(_ context.Context, orgID, projectID, appID string, cfg zitadel.OIDCConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("UpdateOIDCApp")
	a, err := f.app(orgID, projectID, appID)
	if err != nil {
		return err
	}
	cfg.ClientID = a.app.OIDCConfig.ClientID
	a.app.OIDCConfig = &cfg
	return nil
}

func (f *fakeZitadel) UpdateAPIApp(_ context.Context, orgID, projectID, appID string, cfg zitadel.APIConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, err := f.app(orgID, projectID, appID)
	if err != nil {
		return err
	}
	cfg.ClientID = a.app.APIConfig.ClientID
	a.app.APIConfig = &cfg
	return nil
}

func (f *fakeZitadel) RenameApp(_ context.Context, orgID, projectID, appID, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, err := f.app(orgID, projectID, appID)
	if err != nil {
		return err
	}
	a.app.Name = name
	return nil
}

func (f *fakeZitadel) RegenerateClientSecret(_ context.Context, orgID, projectID, appID string, _ bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.track("RegenerateClientSecret")
	a, err := f.app(orgID, projectID, appID)
	if err != nil {
		return "", err
	}
	a.secret = f.id("secret-")
	return a.secret, nil
}

func (f *fakeZitadel) AddAppKey(_ context.Context, orgID, projectID, appID string) (*zitadel.AppKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, err := f.app(orgID, projectID, appID)
	if err != nil {
		return nil, err
	}
	id := f.id("key-")
	a.keys[id] = true
	return &zitadel.AppKey{ID: id, JSON: []byte(`{"keyId":"` + id + `"}`)}, nil
}

func (f *fakeZitadel) RemoveAppKey(_ context.Context, orgID, projectID, appID, keyID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, err := f.app(orgID, projectID, appID)
	if err != nil {
		return err
	}
	delete(a.keys, keyID)
	return nil
}

func (f *fakeZitadel) DeleteApp(_ context.Context, orgID, projectID, appID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.project(orgID, projectID)
	if err != nil {
		return err
	}
	if _, ok := p.apps[appID]; !ok {
		return notFound()
	}
	delete(p.apps, appID)
	return nil
}
