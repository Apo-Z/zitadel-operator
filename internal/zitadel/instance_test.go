package zitadel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// TestInstanceAPIRequests checks methods, paths, organization header and
// response decoding of the instance configuration calls.
func TestInstanceAPIRequests(t *testing.T) {
	type call struct{ method, path, org, body string }
	var calls []call
	responses := map[string]string{
		"POST /v2/organizations/_search":                  `{"result":[{"id":"o1","name":"noor"}]}`,
		"POST /v2/organizations":                          `{"organizationId":"o2"}`,
		"GET /admin/v1/orgs/default":                      `{"org":{"id":"o1"}}`,
		"GET /management/v1/policies/login":               `{"policy":{"isDefault":true,"allowRegister":true}}`,
		"PUT /management/v1/policies/lockout":             `{"code":3,"message":"Errors.Org.LockoutPolicy.NotChanged (COMMAND-0e2f1)"}`,
		"GET /management/v1/flows/2":                      `{"flow":{"triggerActions":[{"triggerType":{"id":"4"},"actions":[{"id":"a1"},{"id":"a2"}]}]}}`,
		"POST /admin/v1/email/_search":                    `{"result":[{"id":"e1","state":"EMAIL_PROVIDER_ACTIVE","description":"ovh","smtp":{"host":"smtp:465","tls":true}}]}`,
		"POST /management/v1/policies/login/idps/_search": `{"result":[{"idpId":"i1","idpName":"google"}]}`,
		"POST /management/v1/policies/login/idps":         `{"code":6,"message":"Errors.Org.LoginPolicy.IDP.AlreadyExists"}`,
		"POST /management/v1/idps/templates/_search":      `{"result":[{"id":"i1","name":"google","config":{"options":{"isLinkingAllowed":true},"google":{"clientId":"c1","scopes":["openid"]}}}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		calls = append(calls, call{r.Method, r.URL.Path, r.Header.Get("x-zitadel-orgid"), string(b)})
		resp, ok := responses[key]
		if !ok {
			resp = `{"id":"new"}`
		}
		if key == "PUT /management/v1/policies/lockout" {
			w.WriteHeader(http.StatusBadRequest)
		}
		if key == "POST /management/v1/policies/login/idps" {
			w.WriteHeader(http.StatusConflict)
		}
		_, _ = w.Write([]byte(resp))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Issuer: srv.URL, Credentials: []byte("pat")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	orgs, err := c.ListOrganizations(ctx)
	if err != nil || len(orgs) != 1 || orgs[0].Name != "noor" {
		t.Fatalf("ListOrganizations: %v %v", orgs, err)
	}
	if id, err := c.CreateOrganization(ctx, "x"); err != nil || id != "o2" {
		t.Fatalf("CreateOrganization: %v %v", id, err)
	}
	if id, err := c.GetDefaultOrganization(ctx); err != nil || id != "o1" {
		t.Fatalf("GetDefaultOrganization: %v %v", id, err)
	}
	if err := c.SetDefaultOrganization(ctx, "o2"); err != nil {
		t.Fatal(err)
	}
	pol, isDefault, err := c.GetPolicy(ctx, "o1", PolicyLogin)
	if err != nil || !isDefault || pol["allowRegister"] != true {
		t.Fatalf("GetPolicy: %v %v %v", pol, isDefault, err)
	}
	if err := c.SetPolicy(ctx, "o1", PolicyLogin, map[string]any{"allowRegister": false}, true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPolicy(ctx, "o1", PolicyLockout, map[string]any{"maxPasswordAttempts": 5}, false); err != nil {
		t.Fatalf("NotChanged must be ignored: %v", err)
	}
	idps, err := c.ListIDPs(ctx, "o1")
	if err != nil || len(idps) != 1 || idps[0].Config.Google.ClientID != "c1" || idps[0].Config.Options["isLinkingAllowed"] != true {
		t.Fatalf("ListIDPs: %+v %v", idps, err)
	}
	if _, err := c.AddGoogleIDP(ctx, "o1", GoogleIDP{Name: "google", ClientID: "c", ClientSecret: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateGoogleIDP(ctx, "o1", "i1", GoogleIDP{Name: "google", ClientID: "c"}); err != nil {
		t.Fatal(err)
	}
	linked, err := c.ListLoginPolicyIDPs(ctx, "o1")
	if err != nil || !slices.Equal(linked, []string{"i1"}) {
		t.Fatalf("ListLoginPolicyIDPs: %v %v", linked, err)
	}
	if err := c.AddIDPToLoginPolicy(ctx, "o1", "i1"); err != nil {
		t.Fatalf("AlreadyExists must be ignored: %v", err)
	}
	triggers, err := c.GetFlowTriggers(ctx, "o1", "2")
	if err != nil || !slices.Equal(triggers["4"], []string{"a1", "a2"}) {
		t.Fatalf("GetFlowTriggers: %v %v", triggers, err)
	}
	if err := c.SetTriggerActions(ctx, "o1", "2", "5", []string{"a1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateAction(ctx, "o1", Action{ID: "ignored", Name: "a", Script: "s", Timeout: "10s"}); err != nil {
		t.Fatal(err)
	}
	providers, err := c.ListEmailProviders(ctx)
	if err != nil || len(providers) != 1 || providers[0].SMTP.Host != "smtp:465" || providers[0].State != EmailProviderActive {
		t.Fatalf("ListEmailProviders: %+v %v", providers, err)
	}
	if err := c.UpdateSMTPProvider(ctx, "e1", SMTPConfig{Host: "smtp:465"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ActivateEmailProvider(ctx, "e1"); err != nil {
		t.Fatal(err)
	}

	want := []call{
		{"POST", "/v2/organizations/_search", "", ""},
		{"POST", "/v2/organizations", "", `{"name":"x"}`},
		{"GET", "/admin/v1/orgs/default", "", ""},
		{"PUT", "/admin/v1/orgs/default/o2", "", "{}"},
		{"GET", "/management/v1/policies/login", "o1", ""},
		{"POST", "/management/v1/policies/login", "o1", `{"allowRegister":false}`},
		{"PUT", "/management/v1/policies/lockout", "o1", `{"maxPasswordAttempts":5}`},
		{"POST", "/management/v1/idps/templates/_search", "o1", ""},
		{"POST", "/management/v1/idps/google", "o1", `{"name":"google","clientId":"c","clientSecret":"s","scopes":null,"providerOptions":null}`},
		{"PUT", "/management/v1/idps/google/i1", "o1", `{"name":"google","clientId":"c","scopes":null,"providerOptions":null}`},
		{"POST", "/management/v1/policies/login/idps/_search", "o1", ""},
		{"POST", "/management/v1/policies/login/idps", "o1", `{"idpId":"i1","ownerType":"IDP_OWNER_TYPE_ORG"}`},
		{"GET", "/management/v1/flows/2", "o1", ""},
		{"POST", "/management/v1/flows/2/trigger/5", "o1", `{"actionIds":["a1"]}`},
		{"POST", "/management/v1/actions", "o1", `{"name":"a","script":"s","timeout":"10s","allowedToFail":false}`},
		{"POST", "/admin/v1/email/_search", "", "{}"},
		{"PUT", "/admin/v1/email/smtp/e1", "", ""},
		{"POST", "/admin/v1/email/e1/_activate", "", "{}"},
	}
	if len(calls) != len(want) {
		t.Fatalf("got %d calls, want %d: %+v", len(calls), len(want), calls)
	}
	for i, w := range want {
		g := calls[i]
		if g.method != w.method || g.path != w.path || g.org != w.org {
			t.Errorf("call %d: got %s %s (org %q), want %s %s (org %q)", i, g.method, g.path, g.org, w.method, w.path, w.org)
		}
		if w.body != "" && !jsonEqual(g.body, w.body) {
			t.Errorf("call %d %s: body %s, want %s", i, w.path, g.body, w.body)
		}
	}
	// The SMTP update without password must not send an empty password.
	var smtpBody map[string]any
	_ = json.Unmarshal([]byte(calls[14].body), &smtpBody)
	if _, ok := smtpBody["password"]; ok {
		t.Error("empty SMTP password sent")
	}
}

func jsonEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}
