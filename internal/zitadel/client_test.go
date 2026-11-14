package zitadel

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func serviceAccountKey(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	raw, _ := json.Marshal(ServiceAccountKey{Type: "serviceaccount", KeyID: "kid-1", Key: string(pemKey), UserID: "user-1"})
	return raw, priv
}

func TestJWTProfileAndInternalURL(t *testing.T) {
	raw, priv := serviceAccountKey(t)
	var tokenCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "auth.example.com" {
			t.Errorf("unexpected Host header %q", r.Host)
		}
		switch r.URL.Path {
		case "/oauth/v2/token":
			tokenCalls.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				t.Errorf("bad grant type %q", r.Form.Get("grant_type"))
			}
			tok, err := jwt.Parse(r.Form.Get("assertion"), func(*jwt.Token) (any, error) { return &priv.PublicKey, nil },
				jwt.WithAudience("https://auth.example.com"), jwt.WithIssuer("user-1"))
			if err != nil {
				t.Errorf("invalid assertion: %v", err)
			}
			if tok != nil && tok.Header["kid"] != "kid-1" {
				t.Errorf("bad kid %v", tok.Header["kid"])
			}
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
		case "/management/v1/orgs/me":
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("bad authorization header %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("x-zitadel-orgid") != "org-1" {
				t.Errorf("bad org header %q", r.Header.Get("x-zitadel-orgid"))
			}
			_, _ = w.Write([]byte(`{"org":{"id":"org-1","name":"ACME"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := NewClient(Config{Issuer: "https://auth.example.com/", APIURL: srv.URL, Credentials: raw})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		org, err := c.GetMyOrg(context.Background(), "org-1")
		if err != nil {
			t.Fatal(err)
		}
		if org.ID != "org-1" || org.Name != "ACME" {
			t.Fatalf("unexpected org %+v", org)
		}
	}
	if n := tokenCalls.Load(); n != 1 {
		t.Fatalf("token should be cached, got %d token requests", n)
	}
}

func TestPATAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer my-pat" {
			t.Errorf("bad authorization header %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/management/v1/projects/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":5,"message":"Project not found (PROJECT-3M9sd)"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/management/v1/projects/p1":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":9,"message":"No changes (COMMAND-1m88i)"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/management/v1/projects/p1/apps/oidc":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "web" || body["appType"] != "OIDC_APP_TYPE_WEB" {
				t.Errorf("unexpected body %v", body)
			}
			_, _ = w.Write([]byte(`{"appId":"a1","clientId":"c1@p","clientSecret":"s3cr3t"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/management/v1/projects/p1/apps/a1/keys":
			_, _ = w.Write([]byte(`{"id":"k1","keyDetails":"eyJrZXlJZCI6ImsxIn0="}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}
	}))
	defer srv.Close()

	c, err := NewClient(Config{Issuer: srv.URL, Credentials: []byte("  my-pat\n")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := c.GetProject(ctx, "", "missing"); !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := c.UpdateProject(ctx, "", "p1", ProjectSettings{Name: "x"}); err != nil {
		t.Fatalf("no changes must be ignored, got %v", err)
	}
	creds, err := c.CreateOIDCApp(ctx, "", "p1", "web", OIDCConfig{AppType: "OIDC_APP_TYPE_WEB"})
	if err != nil || creds.ClientSecret != "s3cr3t" || creds.AppID != "a1" {
		t.Fatalf("unexpected result %+v %v", creds, err)
	}
	key, err := c.AddAppKey(ctx, "", "p1", "a1")
	if err != nil || key.ID != "k1" || string(key.JSON) != `{"keyId":"k1"}` {
		t.Fatalf("unexpected key %+v %v", key, err)
	}
	err = c.DeleteProject(ctx, "", "p2")
	if err == nil || !strings.Contains(err.Error(), "boom") || IsNotFound(err) {
		t.Fatalf("unexpected error %v", err)
	}
}
