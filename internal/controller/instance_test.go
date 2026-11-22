package controller

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

func raw(s string) *runtime.RawExtension { return &runtime.RawExtension{Raw: []byte(s)} }

func idpSecret(id, secret string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "google-idp", Namespace: "zitadel"},
		Data:       map[string][]byte{"CLIENT_ID": []byte(id), "CLIENT_SECRET": []byte(secret)},
	}
}

func newOrganization() *zv1.ZitadelOrganization {
	return &zv1.ZitadelOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: "noor"},
		Spec: zv1.ZitadelOrganizationSpec{
			ConnectionRef:            zv1.ConnectionReference{Name: "main"},
			Default:                  true,
			PasswordComplexityPolicy: raw(`{"minLength": 8, "hasNumber": true}`),
			LoginPolicy:              raw(`{"allowRegister": true, "secondFactors": ["SECOND_FACTOR_TYPE_OTP", "SECOND_FACTOR_TYPE_U2F"]}`),
			IdentityProviders: []zv1.IdentityProvider{{
				Name: "google", Type: zv1.IdentityProviderTypeGoogle,
				CredentialsSecretRef: zv1.IdentityProviderCredentials{Name: "google-idp", Namespace: "zitadel"},
				Scopes:               []string{"openid", "email"},
				Options:              raw(`{"isLinkingAllowed": true, "autoLinking": "AUTO_LINKING_OPTION_EMAIL"}`),
			}},
			Actions: []zv1.Action{{
				Name: "groupsClaim", Script: "function groupsClaim(ctx, api) {}",
				Triggers: []zv1.ActionTrigger{{FlowType: "2", TriggerTypes: []string{"4", "5"}}},
			}},
		},
	}
}

func writes(e *env) int {
	n := 0
	for k, v := range e.z.calls {
		if k != "CreateProject" {
			n += v
		}
	}
	return n
}

func TestOrganizationLifecycle(t *testing.T) {
	e := newEnv(t, idpSecret("client-1", "secret-1"), newOrganization())
	e.reconcile(e.org, "", "noor")

	var org zv1.ZitadelOrganization
	e.get(&org, "", "noor")
	if c := ready(org.Status.Conditions); c.Status != metav1.ConditionTrue {
		t.Fatalf("organization not ready: %+v", c)
	}
	in := e.z.instance
	id := org.Status.OrganizationID
	if in.orgs[id] != "noor" || in.defaultOrg != id {
		t.Fatalf("organization not created/default: %+v default %s", in.orgs, in.defaultOrg)
	}
	if in.policies[id+"/password/complexity"]["minLength"] != float64(8) || in.policies[id+"/lockout"] != nil {
		t.Fatalf("unexpected policies %+v", in.policies)
	}
	var idp *fakeIDP
	for _, i := range in.idps[id] {
		idp = i
	}
	if idp == nil || idp.idp.ClientID != "client-1" || idp.secret != "secret-1" || idp.idp.ProviderOptions["autoLinking"] != "AUTO_LINKING_OPTION_EMAIL" {
		t.Fatalf("unexpected idp %+v", idp)
	}
	var actionID string
	for aid := range in.actions[id] {
		actionID = aid
	}
	if f := in.flows[id+"/2"]; !slices.Equal(f["4"], []string{actionID}) || !slices.Equal(f["5"], []string{actionID}) {
		t.Fatalf("unexpected triggers %+v", in.flows)
	}
	if org.Status.CredentialsHashes["google"] == "" || org.Status.CredentialsHashes["google"] == "secret-1" {
		t.Fatalf("unexpected credentials hashes %+v", org.Status.CredentialsHashes)
	}

	// Steady state: no write at all (policies returned with int64 as strings are equal).
	in.policies[id+"/password/complexity"]["minLength"] = "8"
	before := writes(e)
	e.reconcile(e.org, "", "noor")
	if writes(e) != before {
		t.Fatalf("writes in steady state: %v", e.z.calls)
	}

	// Drift is corrected.
	in.policies[id+"/login"]["allowRegister"] = false
	idp.idp.Scopes = []string{"openid"}
	e.reconcile(e.org, "", "noor")
	if in.policies[id+"/login"]["allowRegister"] != true || len(idp.idp.Scopes) != 2 || idp.secret != "secret-1" {
		t.Fatalf("drift not corrected: %+v %+v", in.policies[id+"/login"], idp)
	}

	// Rotated client secret is sent again.
	s := idpSecret("client-1", "secret-2")
	if err := e.c.Update(e.ctx, s); err != nil {
		t.Fatal(err)
	}
	e.reconcile(e.org, "", "noor")
	if idp.secret != "secret-2" {
		t.Fatal("rotated secret not sent")
	}

	// An action set by someone else on the same trigger is kept.
	in.flows[id+"/2"]["4"] = append(in.flows[id+"/2"]["4"], "external-action")
	e.reconcile(e.org, "", "noor")
	if !slices.Contains(in.flows[id+"/2"]["4"], "external-action") {
		t.Fatal("unmanaged action removed from trigger")
	}

	// Removed from the spec: managed action and identity provider are deleted.
	e.get(&org, "", "noor")
	org.Spec.Actions, org.Spec.IdentityProviders = nil, nil
	if err := e.c.Update(e.ctx, &org); err != nil {
		t.Fatal(err)
	}
	e.reconcile(e.org, "", "noor")
	if len(in.actions[id]) != 0 || len(in.idps[id]) != 0 || !slices.Equal(in.flows[id+"/2"]["4"], []string{"external-action"}) {
		t.Fatalf("not cleaned up: actions %+v idps %+v flows %+v", in.actions[id], in.idps[id], in.flows)
	}

	// Deletion with the default Retain policy keeps the organization.
	if err := e.c.Delete(e.ctx, &org); err != nil {
		t.Fatal(err)
	}
	e.reconcile(e.org, "", "noor")
	if in.orgs[id] != "noor" {
		t.Fatal("organization deleted despite Retain")
	}
}

func TestOrganizationAdoptionAndMissingSecret(t *testing.T) {
	e := newEnv(t, newOrganization())
	e.z.inst().orgs["org-existing"] = "noor"
	res := e.reconcile(e.org, "", "noor")
	var org zv1.ZitadelOrganization
	e.get(&org, "", "noor")
	if org.Status.OrganizationID != "org-existing" || e.z.calls["CreateOrganization"] != 0 {
		t.Fatalf("existing organization not adopted: %+v", org.Status)
	}
	if c := ready(org.Status.Conditions); c.Reason != ReasonSecretNotFound || res.RequeueAfter == 0 {
		t.Fatalf("expected SecretNotFound with requeue, got %+v %+v", c, res)
	}
}

func TestSMTPProvider(t *testing.T) {
	pwd := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "smtp", Namespace: "zitadel"},
		Data:       map[string][]byte{"password": []byte("pwd-1")},
	}
	smtp := &zv1.ZitadelSMTPProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "ovh"},
		Spec: zv1.ZitadelSMTPProviderSpec{
			ConnectionRef:     zv1.ConnectionReference{Name: "main"},
			Host:              "smtp.example.com:465",
			User:              "noreply@example.com",
			PasswordSecretRef: &zv1.SecretKeyReference{Name: "smtp", Namespace: "zitadel", Key: "password"},
			SenderAddress:     "noreply@example.com",
			SenderName:        "Example",
		},
	}
	e := newEnv(t, pwd, smtp)
	e.reconcile(e.smtp, "", "ovh")
	var s zv1.ZitadelSMTPProvider
	e.get(&s, "", "ovh")
	in := e.z.instance
	p := in.email[s.Status.ProviderID]
	if p == nil || p.State != "EMAIL_PROVIDER_ACTIVE" || !p.SMTP.TLS || p.Description != "ovh" || in.emailPwd[p.ID] != "pwd-1" {
		t.Fatalf("unexpected provider %+v", p)
	}

	// Steady state.
	before := writes(e)
	e.reconcile(e.smtp, "", "ovh")
	if writes(e) != before {
		t.Fatalf("writes in steady state: %v", e.z.calls)
	}

	// Password rotation and drift.
	pwd.Data["password"] = []byte("pwd-2")
	if err := e.c.Update(e.ctx, pwd); err != nil {
		t.Fatal(err)
	}
	p.SMTP.SenderName = "changed"
	e.reconcile(e.smtp, "", "ovh")
	if in.emailPwd[p.ID] != "pwd-2" || p.SMTP.SenderName != "Example" {
		t.Fatalf("not updated: %+v password %q", p.SMTP, in.emailPwd[p.ID])
	}
}

func TestSMTPProviderAdoptsByHost(t *testing.T) {
	smtp := &zv1.ZitadelSMTPProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "ovh"},
		Spec: zv1.ZitadelSMTPProviderSpec{
			ConnectionRef: zv1.ConnectionReference{Name: "main"},
			Host:          "smtp.example.com:465", SenderAddress: "noreply@example.com", SenderName: "Example",
		},
	}
	e := newEnv(t, smtp)
	id, _ := e.z.AddSMTPProvider(e.ctx, zitadelSMTP("old", "smtp.example.com:465"))
	e.reconcile(e.smtp, "", "ovh")
	var s zv1.ZitadelSMTPProvider
	e.get(&s, "", "ovh")
	if s.Status.ProviderID != id || len(e.z.instance.email) != 1 || e.z.instance.email[id].Description != "ovh" {
		t.Fatalf("provider not adopted: %+v", s.Status)
	}
}

func zitadelSMTP(desc, host string) zitadel.SMTPConfig {
	return zitadel.SMTPConfig{Description: desc, Host: host, SenderAddress: "x@example.com"}
}
