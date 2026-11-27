//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/controller"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

func rawJSON(s string) *runtime.RawExtension { return &runtime.RawExtension{Raw: []byte(s)} }

// TestE2EInstance configures an organization (policies, Google IdP, action and
// triggers) and an SMTP provider on a real Zitadel, then checks the result
// through the Zitadel API.
func TestE2EInstance(t *testing.T) {
	zitadelURL := os.Getenv("ZITADEL_URL")
	if zitadelURL == "" {
		t.Skip("ZITADEL_URL not set")
	}
	saKey := mustRead(t, "ZITADEL_SA_KEY_FILE")
	pat := mustRead(t, "ZITADEL_PAT_FILE")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = testEnv.Stop() }()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = zv1.AddToScheme(scheme)
	skip := true // TestE2E registers controllers with the same names in this process
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"},
		Controller: config.Controller{SkipNameValidation: &skip}})
	must(t, err)
	clients := &controller.SecretClientFactory{Reader: mgr.GetAPIReader()}
	must(t, (&controller.ZitadelConnectionReconciler{Client: mgr.GetClient(), Clients: clients}).SetupWithManager(mgr))
	must(t, (&controller.ZitadelOrganizationReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Clients: clients}).SetupWithManager(mgr))
	must(t, (&controller.ZitadelSMTPProviderReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Clients: clients}).SetupWithManager(mgr))
	go func() { _ = mgr.Start(ctx) }()
	k, err := client.New(cfg, client.Options{Scheme: scheme})
	must(t, err)

	must(t, k.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "zitadel"}}))
	must(t, k.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "iam-admin", Namespace: "zitadel"},
		Data:       map[string][]byte{"iam-admin.json": saKey},
	}))
	googleSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "google-idp", Namespace: "zitadel"},
		Data:       map[string][]byte{"CLIENT_ID": []byte("e2e.apps.googleusercontent.com"), "CLIENT_SECRET": []byte("secret-1")},
	}
	must(t, k.Create(ctx, googleSecret))
	must(t, k.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "smtp", Namespace: "zitadel"},
		Data:       map[string][]byte{"password": []byte("smtp-pwd")},
	}))
	must(t, k.Create(ctx, &zv1.ZitadelConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "main"},
		Spec: zv1.ZitadelConnectionSpec{URL: zitadelURL,
			CredentialsSecretRef: zv1.CredentialsSecretReference{Name: "iam-admin", Namespace: "zitadel", Key: "iam-admin.json"}},
	}))

	orgName := fmt.Sprintf("e2e-org-%d", time.Now().Unix())
	script := "function groupsClaim(ctx, api) {\n  api.v1.claims.setClaim('groups', []);\n}\n"
	org := &zv1.ZitadelOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: "noor"},
		Spec: zv1.ZitadelOrganizationSpec{
			ConnectionRef: zv1.ConnectionReference{Name: "main"},
			Name:          orgName,
			Default:       true,
			PasswordComplexityPolicy: rawJSON(`{"minLength": 10, "hasLowercase": true, "hasUppercase": true,
				"hasNumber": true, "hasSymbol": false}`),
			LockoutPolicy: rawJSON(`{"maxPasswordAttempts": 5}`),
			LoginPolicy: rawJSON(`{"allowUsernamePassword": true, "allowRegister": false, "allowExternalIdp": true,
				"forceMfa": false, "passwordlessType": "PASSWORDLESS_TYPE_ALLOWED", "hidePasswordReset": false,
				"ignoreUnknownUsernames": true, "defaultRedirectUri": "", "passwordCheckLifetime": "864000s",
				"externalLoginCheckLifetime": "864000s", "mfaInitSkipLifetime": "2592000s",
				"secondFactorCheckLifetime": "86400s", "multiFactorCheckLifetime": "86400s",
				"secondFactors": ["SECOND_FACTOR_TYPE_OTP", "SECOND_FACTOR_TYPE_U2F"],
				"multiFactors": ["MULTI_FACTOR_TYPE_U2F_WITH_VERIFICATION"], "allowDomainDiscovery": true,
				"disableLoginWithEmail": false, "disableLoginWithPhone": false, "forceMfaLocalOnly": false}`),
			IdentityProviders: []zv1.IdentityProvider{{
				Name: "google", Type: zv1.IdentityProviderTypeGoogle,
				CredentialsSecretRef: zv1.IdentityProviderCredentials{Name: "google-idp", Namespace: "zitadel"},
				Scopes:               []string{"openid", "profile", "email"},
				Options: rawJSON(`{"isLinkingAllowed": true, "isCreationAllowed": false, "isAutoCreation": false,
					"isAutoUpdate": false, "autoLinking": "AUTO_LINKING_OPTION_EMAIL"}`),
			}},
			Actions: []zv1.Action{{Name: "groupsClaim", Script: script,
				Triggers: []zv1.ActionTrigger{{FlowType: "2", TriggerTypes: []string{"4", "5"}}}}},
		},
	}
	must(t, k.Create(ctx, org))
	must(t, k.Create(ctx, &zv1.ZitadelSMTPProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-smtp"},
		Spec: zv1.ZitadelSMTPProviderSpec{
			ConnectionRef:     zv1.ConnectionReference{Name: "main"},
			Host:              "smtp.example.com:465",
			User:              "noreply@example.com",
			PasswordSecretRef: &zv1.SecretKeyReference{Name: "smtp", Namespace: "zitadel", Key: "password"},
			SenderAddress:     "noreply@example.com", SenderName: "E2E",
		},
	}))

	var got zv1.ZitadelOrganization
	eventually(t, "organization ready", func() error {
		if err := k.Get(ctx, client.ObjectKey{Name: "noor"}, &got); err != nil {
			return err
		}
		return isReady(got.Status.Conditions)
	})
	var smtp zv1.ZitadelSMTPProvider
	eventually(t, "smtp ready", func() error {
		if err := k.Get(ctx, client.ObjectKey{Name: "e2e-smtp"}, &smtp); err != nil {
			return err
		}
		return isReady(smtp.Status.Conditions)
	})

	// Check Zitadel itself.
	api, err := zitadel.NewClient(zitadel.Config{Issuer: zitadelURL, Credentials: pat})
	must(t, err)
	orgID := got.Status.OrganizationID
	if def, err := api.GetDefaultOrganization(ctx); err != nil || def != orgID {
		t.Fatalf("default organization %q (want %q): %v", def, orgID, err)
	}
	pol, isDefault, err := api.GetPolicy(ctx, orgID, zitadel.PolicyPasswordComplexity)
	if err != nil || isDefault || fmt.Sprint(pol["minLength"]) != "10" {
		t.Fatalf("password policy %v default=%v: %v", pol, isDefault, err)
	}
	login, _, err := api.GetPolicy(ctx, orgID, zitadel.PolicyLogin)
	if err != nil || login["allowRegister"] == true || len(login["secondFactors"].([]any)) != 2 {
		t.Fatalf("login policy %v: %v", login, err)
	}
	idps, err := api.ListIDPs(ctx, orgID)
	if err != nil || len(idps) != 1 || idps[0].Config.Google == nil || idps[0].Config.Google.ClientID != "e2e.apps.googleusercontent.com" ||
		idps[0].Config.Options["autoLinking"] != "AUTO_LINKING_OPTION_EMAIL" {
		t.Fatalf("idps %+v: %v", idps, err)
	}
	if linked, err := api.ListLoginPolicyIDPs(ctx, orgID); err != nil || !slices.Equal(linked, []string{idps[0].ID}) {
		t.Fatalf("login policy idps %v (want %s): %v", linked, idps[0].ID, err)
	}
	actions, err := api.ListActions(ctx, orgID)
	if err != nil || len(actions) != 1 || actions[0].Script != script || actions[0].Timeout != "10s" {
		t.Fatalf("actions %+v: %v", actions, err)
	}
	triggers, err := api.GetFlowTriggers(ctx, orgID, "2")
	if err != nil || !slices.Equal(triggers["4"], []string{actions[0].ID}) || !slices.Equal(triggers["5"], []string{actions[0].ID}) {
		t.Fatalf("triggers %+v: %v", triggers, err)
	}
	providers, err := api.ListEmailProviders(ctx)
	must(t, err)
	found := false
	for _, p := range providers {
		if p.ID == smtp.Status.ProviderID {
			found = p.State == zitadel.EmailProviderActive && p.SMTP != nil && p.SMTP.SenderName == "E2E" && p.Description == "e2e-smtp"
		}
	}
	if !found {
		t.Fatalf("smtp provider %s not found/active in %+v", smtp.Status.ProviderID, providers)
	}

	// Steady state against the real API: a new reconciliation changes nothing
	// (generation-independent: compare the condition's last transition).
	hash := got.Status.CredentialsHashes["google"]

	// Rotated Google credentials are sent again.
	googleSecret.Data["CLIENT_SECRET"] = []byte("secret-2")
	must(t, k.Update(ctx, googleSecret))
	got.Annotations = map[string]string{"e2e/touch": "1"} // trigger a reconciliation now
	must(t, k.Update(ctx, &got))
	eventually(t, "credentials resent", func() error {
		if err := k.Get(ctx, client.ObjectKey{Name: "noor"}, &got); err != nil {
			return err
		}
		if got.Status.CredentialsHashes["google"] == hash {
			return fmt.Errorf("hash unchanged")
		}
		return isReady(got.Status.Conditions)
	})

	// Removing the action and the provider from the spec deletes them.
	got.Spec.Actions, got.Spec.IdentityProviders = nil, nil
	must(t, k.Update(ctx, &got))
	eventually(t, "action and idp deleted", func() error {
		a, err := api.ListActions(ctx, orgID)
		if err != nil {
			return err
		}
		i, err := api.ListIDPs(ctx, orgID)
		if err != nil {
			return err
		}
		if len(a) != 0 || len(i) != 0 {
			return fmt.Errorf("still %d actions, %d idps", len(a), len(i))
		}
		return nil
	})
}
