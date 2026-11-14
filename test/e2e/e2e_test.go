//go:build e2e

// Package e2e runs the operator against a real Zitadel instance and a real
// kube-apiserver (envtest).
//
//	ZITADEL_URL=http://localhost:18080 ZITADEL_SA_KEY_FILE=sa.json ZITADEL_PAT_FILE=pat.txt \
//	KUBEBUILDER_ASSETS=$(setup-envtest use -p path) go test -tags e2e ./test/e2e/ -v
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/controller"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

const ns = "my-app"

func mustRead(t *testing.T, env string) []byte {
	t.Helper()
	path := os.Getenv(env)
	if path == "" {
		t.Skipf("%s not set", env)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func eventually(t *testing.T, what string, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if err = fn(); err == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %v", what, err)
}

func isReady(conds []metav1.Condition) error {
	c := meta.FindStatusCondition(conds, zv1.ConditionReady)
	if c == nil || c.Status != metav1.ConditionTrue {
		return fmt.Errorf("not ready: %+v", c)
	}
	return nil
}

func TestE2E(t *testing.T) {
	zitadelURL := os.Getenv("ZITADEL_URL")
	if zitadelURL == "" {
		t.Skip("ZITADEL_URL not set")
	}
	saKey := mustRead(t, "ZITADEL_SA_KEY_FILE")
	pat := mustRead(t, "ZITADEL_PAT_FILE")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

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
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Cache: cache.Options{ByObject: map[client.Object]cache.ByObject{
			&corev1.Secret{}: {Label: labels.SelectorFromSet(labels.Set{controller.ManagedByLabel: controller.ManagedByValue})},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	clients := &controller.SecretClientFactory{Reader: mgr.GetAPIReader()}
	must(t, (&controller.ZitadelConnectionReconciler{Client: mgr.GetClient(), Clients: clients}).SetupWithManager(mgr))
	must(t, (&controller.ZitadelProjectReconciler{Client: mgr.GetClient(), Clients: clients}).SetupWithManager(mgr))
	must(t, (&controller.ZitadelApplicationReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Clients: clients}).SetupWithManager(mgr))
	go func() { _ = mgr.Start(ctx) }()

	k, err := client.New(cfg, client.Options{Scheme: scheme})
	must(t, err)

	// Credentials and connection (JWT profile through an "internal" URL).
	u, _ := url.Parse(zitadelURL)
	internal := fmt.Sprintf("%s://127.0.0.1:%s", u.Scheme, u.Port())
	for _, n := range []string{"zitadel-operator", ns, "other"} {
		must(t, k.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: n}}))
	}
	must(t, k.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "zitadel-admin", Namespace: "zitadel-operator"},
		Data:       map[string][]byte{"key.json": saKey, "pat": pat},
	}))
	must(t, k.Create(ctx, &zv1.ZitadelConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "main"},
		Spec: zv1.ZitadelConnectionSpec{
			URL: zitadelURL, InternalURL: internal,
			CredentialsSecretRef: zv1.CredentialsSecretReference{Name: "zitadel-admin", Namespace: "zitadel-operator"},
			AllowedNamespaces:    []string{"my-*"},
		},
	}))
	must(t, k.Create(ctx, &zv1.ZitadelConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "pat"},
		Spec: zv1.ZitadelConnectionSpec{
			URL:                  zitadelURL,
			CredentialsSecretRef: zv1.CredentialsSecretReference{Name: "zitadel-admin", Namespace: "zitadel-operator", Key: "pat"},
		},
	}))
	for _, name := range []string{"main", "pat"} {
		eventually(t, "connection "+name, func() error {
			var c zv1.ZitadelConnection
			if err := k.Get(ctx, client.ObjectKey{Name: name}, &c); err != nil {
				return err
			}
			return isReady(c.Status.Conditions)
		})
	}

	// CEL validation of the CRD.
	bad := &zv1.ZitadelApplication{
		ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: ns},
		Spec:       zv1.ZitadelApplicationSpec{ProjectID: "123"},
	}
	if err := k.Create(ctx, bad); err == nil || !strings.Contains(err.Error(), "connectionRef is required") {
		t.Fatalf("expected CEL validation error, got %v", err)
	}

	projectName := fmt.Sprintf("e2e-%d", time.Now().Unix())
	must(t, k.Create(ctx, &zv1.ZitadelProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: ns},
		Spec: zv1.ZitadelProjectSpec{
			ConnectionRef: zv1.ConnectionReference{Name: "main"},
			Name:          projectName,
			Roles:         []zv1.ProjectRole{{Key: "admin", DisplayName: "Admin"}, {Key: "user"}},
		},
	}))
	webApp := &zv1.ZitadelApplication{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-web", Namespace: ns},
		Spec: zv1.ZitadelApplicationSpec{
			ProjectRef: &zv1.LocalObjectReference{Name: "shop"},
			OIDC: &zv1.OIDCConfig{
				RedirectURIs: []string{"https://shop.example.com/callback"},
				GrantTypes:   []zv1.OIDCGrantType{"AUTHORIZATION_CODE", "REFRESH_TOKEN"},
			},
			Secret: zv1.ApplicationSecretSpec{Template: map[string]string{
				"config.yaml": "issuer: {{ .Issuer }}\nclientId: {{ .ClientID }}\nclientSecret: {{ .ClientSecret }}\n",
			}},
		},
	}
	must(t, k.Create(ctx, webApp))
	must(t, k.Create(ctx, &zv1.ZitadelApplication{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-api", Namespace: ns},
		Spec: zv1.ZitadelApplicationSpec{
			ProjectRef: &zv1.LocalObjectReference{Name: "shop"},
			Type:       zv1.ApplicationTypeAPI,
		},
	}))
	must(t, k.Create(ctx, &zv1.ZitadelApplication{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-spa", Namespace: ns},
		Spec: zv1.ZitadelApplicationSpec{
			ProjectRef: &zv1.LocalObjectReference{Name: "shop"},
			OIDC: &zv1.OIDCConfig{
				AppType: "USER_AGENT", AuthMethod: zv1.AuthMethodNone, DevMode: true,
				RedirectURIs: []string{"http://localhost:3000/callback"},
			},
		},
	}))

	var proj zv1.ZitadelProject
	eventually(t, "project", func() error {
		if err := k.Get(ctx, client.ObjectKey{Namespace: ns, Name: "shop"}, &proj); err != nil {
			return err
		}
		return isReady(proj.Status.Conditions)
	})
	apps := map[string]*zv1.ZitadelApplication{}
	for _, name := range []string{"shop-web", "shop-api", "shop-spa"} {
		app := &zv1.ZitadelApplication{}
		eventually(t, "application "+name, func() error {
			if err := k.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, app); err != nil {
				return err
			}
			return isReady(app.Status.Conditions)
		})
		apps[name] = app
	}

	// Check Zitadel directly.
	z, err := zitadel.NewClient(zitadel.Config{Issuer: zitadelURL, Credentials: pat})
	must(t, err)
	orgID := proj.Status.OrganizationID
	roles, err := z.ListProjectRoles(ctx, orgID, proj.Status.ProjectID)
	must(t, err)
	if len(roles) != 2 {
		t.Fatalf("expected 2 roles, got %+v", roles)
	}
	zapp, err := z.GetApp(ctx, orgID, proj.Status.ProjectID, apps["shop-web"].Status.AppID)
	must(t, err)
	if zapp.OIDCConfig == nil || len(zapp.OIDCConfig.GrantTypes) != 2 || zapp.OIDCConfig.RedirectURIs[0] != "https://shop.example.com/callback" {
		t.Fatalf("unexpected app %+v", zapp.OIDCConfig)
	}

	// Web app: the client secret in the Secret is accepted by Zitadel.
	var webSecret corev1.Secret
	must(t, k.Get(ctx, client.ObjectKey{Namespace: ns, Name: "shop-web"}, &webSecret))
	var webCfg struct{ Issuer, ClientID, ClientSecret string }
	parseYAMLish(string(webSecret.Data["config.yaml"]), &webCfg)
	if webCfg.ClientID != apps["shop-web"].Status.ClientID || webCfg.ClientSecret == "" || webCfg.Issuer != strings.TrimSuffix(zitadelURL, "/") {
		t.Fatalf("unexpected rendered config %q", webSecret.Data["config.yaml"])
	}
	checkClientSecret(t, zitadelURL, webCfg.ClientID, webCfg.ClientSecret, true)
	checkClientSecret(t, zitadelURL, webCfg.ClientID, "wrong", false)

	// API app: the generated key authenticates against the introspection endpoint.
	var apiSecret corev1.Secret
	must(t, k.Get(ctx, client.ObjectKey{Namespace: ns, Name: "shop-api"}, &apiSecret))
	checkAppKey(t, zitadelURL, apiSecret.Data["key.json"], string(pat))

	// SPA: public client, no secret.
	var spaSecret corev1.Secret
	must(t, k.Get(ctx, client.ObjectKey{Namespace: ns, Name: "shop-spa"}, &spaSecret))
	if _, ok := spaSecret.Data["CLIENT_SECRET"]; ok || len(spaSecret.Data["CLIENT_ID"]) == 0 {
		t.Fatalf("unexpected SPA secret %v", spaSecret.Data)
	}

	// Update: redirect URIs pushed to Zitadel, secret unchanged.
	must(t, k.Get(ctx, client.ObjectKey{Namespace: ns, Name: "shop-web"}, webApp))
	webApp.Spec.OIDC.RedirectURIs = append(webApp.Spec.OIDC.RedirectURIs, "https://shop2.example.com/callback")
	must(t, k.Update(ctx, webApp))
	eventually(t, "redirect URIs update", func() error {
		a, err := z.GetApp(ctx, orgID, proj.Status.ProjectID, webApp.Status.AppID)
		if err != nil {
			return err
		}
		if len(a.OIDCConfig.RedirectURIs) != 2 {
			return fmt.Errorf("redirect URIs %v", a.OIDCConfig.RedirectURIs)
		}
		return nil
	})
	checkClientSecret(t, zitadelURL, webCfg.ClientID, webCfg.ClientSecret, true)

	// Rotation.
	must(t, k.Get(ctx, client.ObjectKey{Namespace: ns, Name: "shop-web"}, webApp))
	webApp.Annotations = map[string]string{zv1.AnnotationRotateSecret: "1"}
	must(t, k.Update(ctx, webApp))
	var rotated struct{ Issuer, ClientID, ClientSecret string }
	eventually(t, "secret rotation", func() error {
		var s corev1.Secret
		if err := k.Get(ctx, client.ObjectKey{Namespace: ns, Name: "shop-web"}, &s); err != nil {
			return err
		}
		parseYAMLish(string(s.Data["config.yaml"]), &rotated)
		if rotated.ClientSecret == webCfg.ClientSecret {
			return fmt.Errorf("not rotated yet")
		}
		return nil
	})
	checkClientSecret(t, zitadelURL, rotated.ClientID, rotated.ClientSecret, true)
	checkClientSecret(t, zitadelURL, webCfg.ClientID, webCfg.ClientSecret, false)

	// Forbidden namespace.
	must(t, k.Create(ctx, &zv1.ZitadelProject{
		ObjectMeta: metav1.ObjectMeta{Name: "nope", Namespace: "other"},
		Spec:       zv1.ZitadelProjectSpec{ConnectionRef: zv1.ConnectionReference{Name: "main"}},
	}))
	eventually(t, "forbidden namespace", func() error {
		var p zv1.ZitadelProject
		if err := k.Get(ctx, client.ObjectKey{Namespace: "other", Name: "nope"}, &p); err != nil {
			return err
		}
		if c := meta.FindStatusCondition(p.Status.Conditions, zv1.ConditionReady); c == nil || c.Reason != controller.ReasonNamespaceForbidden {
			return fmt.Errorf("condition %+v", c)
		}
		return nil
	})

	// Deletion.
	must(t, k.Delete(ctx, apps["shop-spa"]))
	eventually(t, "SPA deletion", func() error {
		_, err := z.GetApp(ctx, orgID, proj.Status.ProjectID, apps["shop-spa"].Status.AppID)
		if zitadel.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("still exists (%v)", err)
	})
	for _, name := range []string{"shop-web", "shop-api"} {
		must(t, k.Delete(ctx, apps[name]))
	}
	must(t, k.Delete(ctx, &proj))
	eventually(t, "project deletion", func() error {
		err := k.Get(ctx, client.ObjectKey{Namespace: ns, Name: "shop"}, &zv1.ZitadelProject{})
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("CR still exists (%v)", err)
		}
		_, err = z.GetProject(ctx, orgID, proj.Status.ProjectID)
		if zitadel.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("still exists in zitadel (%v)", err)
	})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func parseYAMLish(s string, out *struct{ Issuer, ClientID, ClientSecret string }) {
	for _, line := range strings.Split(s, "\n") {
		k, v, _ := strings.Cut(line, ": ")
		switch k {
		case "issuer":
			out.Issuer = v
		case "clientId":
			out.ClientID = v
		case "clientSecret":
			out.ClientSecret = v
		}
	}
}

// checkClientSecret exchanges a bogus authorization code: Zitadel answers
// invalid_grant when the client authenticated, invalid_client otherwise.
func checkClientSecret(t *testing.T, issuer, clientID, secret string, valid bool) {
	t.Helper()
	form := url.Values{"grant_type": {"authorization_code"}, "code": {"bogus"}, "redirect_uri": {"https://shop.example.com/callback"}}
	req, _ := http.NewRequest(http.MethodPost, strings.TrimSuffix(issuer, "/")+"/oauth/v2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	defer resp.Body.Close()
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	authenticated := body.Error != "invalid_client" && body.Error != "unauthorized_client"
	if authenticated != valid {
		t.Fatalf("client secret valid=%v expected %v (error %q)", authenticated, valid, body.Error)
	}
}

// checkAppKey authenticates on the introspection endpoint with private_key_jwt.
func checkAppKey(t *testing.T, issuer string, keyJSON []byte, token string) {
	t.Helper()
	var key struct {
		KeyID    string `json:"keyId"`
		Key      string `json:"key"`
		ClientID string `json:"clientId"`
	}
	must(t, json.Unmarshal(keyJSON, &key))
	priv, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(key.Key))
	must(t, err)
	issuer = strings.TrimSuffix(issuer, "/")
	now := time.Now()
	assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer: key.ClientID, Subject: key.ClientID, Audience: jwt.ClaimStrings{issuer},
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	})
	assertion.Header["kid"] = key.KeyID
	signed, err := assertion.SignedString(priv)
	must(t, err)
	form := url.Values{
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {signed},
		"token":                 {strings.TrimSpace(token)},
	}
	resp, err := http.PostForm(issuer+"/oauth/v2/introspect", form)
	must(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("introspection with application key failed: HTTP %d", resp.StatusCode)
	}
}
