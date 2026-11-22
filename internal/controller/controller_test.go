package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

const ns = "my-app"

type env struct {
	t    *testing.T
	ctx  context.Context
	c    client.Client
	z    *fakeZitadel
	conn *ZitadelConnectionReconciler
	proj *ZitadelProjectReconciler
	app  *ZitadelApplicationReconciler
	org  *ZitadelOrganizationReconciler
	smtp *ZitadelSMTPProviderReconciler
}

func newEnv(t *testing.T, objs ...client.Object) *env {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = zv1.AddToScheme(scheme)

	base := []client.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "zitadel-admin", Namespace: "zitadel-operator"},
			Data:       map[string][]byte{"key.json": []byte("pat")},
		},
		&zv1.ZitadelConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "main"},
			Spec: zv1.ZitadelConnectionSpec{
				URL:                  "https://auth.example.com/",
				CredentialsSecretRef: zv1.CredentialsSecretReference{Name: "zitadel-admin", Namespace: "zitadel-operator"},
				AllowedNamespaces:    []string{"my-*"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(append(base, objs...)...).
		WithStatusSubresource(&zv1.ZitadelConnection{}, &zv1.ZitadelProject{}, &zv1.ZitadelApplication{},
			&zv1.ZitadelOrganization{}, &zv1.ZitadelSMTPProvider{}).
		Build()
	z := newFakeZitadel()
	e := &env{
		t: t, ctx: context.Background(), c: c, z: z,
		conn: &ZitadelConnectionReconciler{Client: c, Clients: z},
		proj: &ZitadelProjectReconciler{Client: c, Clients: z},
		app:  &ZitadelApplicationReconciler{Client: c, APIReader: c, Clients: z},
		org:  &ZitadelOrganizationReconciler{Client: c, APIReader: c, Clients: z},
		smtp: &ZitadelSMTPProviderReconciler{Client: c, APIReader: c, Clients: z},
	}
	e.reconcile(e.conn, "", "main")
	return e
}

func (e *env) reconcile(r interface {
	Reconcile(context.Context, ctrl.Request) (ctrl.Result, error)
}, namespace, name string) ctrl.Result {
	e.t.Helper()
	res, err := r.Reconcile(e.ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: namespace, Name: name}})
	if err != nil {
		e.t.Fatalf("reconcile %s/%s: %v", namespace, name, err)
	}
	return res
}

func (e *env) get(obj client.Object, namespace, name string) {
	e.t.Helper()
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: namespace, Name: name}, obj); err != nil {
		e.t.Fatalf("get %s/%s: %v", namespace, name, err)
	}
}

func (e *env) secret(name string) *corev1.Secret {
	e.t.Helper()
	var s corev1.Secret
	e.get(&s, ns, name)
	return &s
}

func ready(conds []metav1.Condition) *metav1.Condition {
	return meta.FindStatusCondition(conds, zv1.ConditionReady)
}

func newProject() *zv1.ZitadelProject {
	return &zv1.ZitadelProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: ns},
		Spec: zv1.ZitadelProjectSpec{
			ConnectionRef:        zv1.ConnectionReference{Name: "main"},
			ProjectRoleAssertion: true,
			Roles:                []zv1.ProjectRole{{Key: "admin"}, {Key: "user", DisplayName: "User"}},
		},
	}
}

func newOIDCApp() *zv1.ZitadelApplication {
	return &zv1.ZitadelApplication{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-web", Namespace: ns},
		Spec: zv1.ZitadelApplicationSpec{
			ProjectRef: &zv1.LocalObjectReference{Name: "shop"},
			Type:       zv1.ApplicationTypeOIDC,
			OIDC: &zv1.OIDCConfig{
				RedirectURIs: []string{"https://shop.example.com/callback"},
				GrantTypes:   []zv1.OIDCGrantType{"AUTHORIZATION_CODE", "REFRESH_TOKEN"},
			},
			Secret: zv1.ApplicationSecretSpec{
				Name:     "shop-oidc",
				Template: map[string]string{"OIDC_CLIENT_ID": "{{ .ClientID }}", "OIDC_CLIENT_SECRET": "{{ .ClientSecret }}", "OIDC_ISSUER": "{{ .Issuer }}"},
			},
		},
	}
}

func TestConnection(t *testing.T) {
	e := newEnv(t)
	var conn zv1.ZitadelConnection
	e.get(&conn, "", "main")
	if conn.Status.OrganizationID != "org-1" || ready(conn.Status.Conditions).Status != metav1.ConditionTrue {
		t.Fatalf("unexpected status %+v", conn.Status)
	}
}

func TestConnectionWithoutOrgRead(t *testing.T) {
	e := newEnv(t)
	e.z.noOrgRead = true
	e.reconcile(e.conn, "", "main")
	var conn zv1.ZitadelConnection
	e.get(&conn, "", "main")
	c := ready(conn.Status.Conditions)
	if conn.Status.OrganizationID != "org-1" || c.Status != metav1.ConditionTrue || !strings.Contains(c.Message, "PROJECT_OWNER") {
		t.Fatalf("unexpected status %+v", conn.Status)
	}
}

func TestProjectAndOIDCApplicationLifecycle(t *testing.T) {
	e := newEnv(t, newProject(), newOIDCApp())

	// The application waits for the project.
	res := e.reconcile(e.app, ns, "shop-web")
	var app zv1.ZitadelApplication
	e.get(&app, ns, "shop-web")
	if c := ready(app.Status.Conditions); c.Reason != ReasonProjectNotReady || res.RequeueAfter == 0 {
		t.Fatalf("expected ProjectNotReady, got %+v", c)
	}

	// Project.
	e.reconcile(e.proj, ns, "shop")
	var proj zv1.ZitadelProject
	e.get(&proj, ns, "shop")
	if proj.Status.ProjectID == "" || ready(proj.Status.Conditions).Status != metav1.ConditionTrue {
		t.Fatalf("project not ready: %+v", proj.Status)
	}
	zp := e.z.projects[proj.Status.ProjectID]
	if zp.project.Name != "shop" || !zp.project.ProjectRoleAssertion || len(zp.roles) != 2 || zp.roles["admin"].DisplayName != "admin" {
		t.Fatalf("unexpected zitadel project %+v roles %+v", zp.project, zp.roles)
	}

	// Application + Secret.
	e.reconcile(e.app, ns, "shop-web")
	e.get(&app, ns, "shop-web")
	if c := ready(app.Status.Conditions); c.Status != metav1.ConditionTrue {
		t.Fatalf("app not ready: %+v", c)
	}
	za := zp.apps[app.Status.AppID]
	if za == nil || za.app.OIDCConfig.AppType != "OIDC_APP_TYPE_WEB" || len(za.app.OIDCConfig.GrantTypes) != 2 {
		t.Fatalf("unexpected zitadel app %+v", za)
	}
	s := e.secret("shop-oidc")
	if string(s.Data["OIDC_CLIENT_ID"]) != app.Status.ClientID ||
		string(s.Data["OIDC_CLIENT_SECRET"]) != za.secret ||
		string(s.Data["OIDC_ISSUER"]) != "https://auth.example.com" ||
		len(s.Data) != 3 {
		t.Fatalf("unexpected secret data %v", s.Data)
	}
	if s.Labels[ManagedByLabel] != ManagedByValue || !metav1.IsControlledBy(s, &app) {
		t.Fatalf("secret is not labelled/owned: %+v", s.ObjectMeta)
	}

	// Steady state: nothing is regenerated nor updated.
	e.reconcile(e.app, ns, "shop-web")
	if e.z.calls["RegenerateClientSecret"] != 0 || e.z.calls["UpdateOIDCApp"] != 0 || e.z.calls["CreateApp"] != 1 {
		t.Fatalf("unexpected calls %v", e.z.calls)
	}

	// Spec change: configuration updated, secret not rotated.
	e.get(&app, ns, "shop-web")
	app.Spec.OIDC.RedirectURIs = append(app.Spec.OIDC.RedirectURIs, "https://shop2.example.com/callback")
	if err := e.c.Update(e.ctx, &app); err != nil {
		t.Fatal(err)
	}
	e.reconcile(e.app, ns, "shop-web")
	if len(za.app.OIDCConfig.RedirectURIs) != 2 || e.z.calls["UpdateOIDCApp"] != 1 || e.z.calls["RegenerateClientSecret"] != 0 {
		t.Fatalf("config not updated: %+v calls %v", za.app.OIDCConfig, e.z.calls)
	}

	// Drift in Zitadel is corrected.
	za.app.OIDCConfig.DevMode = true
	e.reconcile(e.app, ns, "shop-web")
	if za.app.OIDCConfig.DevMode {
		t.Fatal("drift not corrected")
	}

	// Rotation via annotation.
	e.get(&app, ns, "shop-web")
	app.Annotations = map[string]string{zv1.AnnotationRotateSecret: "1"}
	if err := e.c.Update(e.ctx, &app); err != nil {
		t.Fatal(err)
	}
	e.reconcile(e.app, ns, "shop-web")
	if s := e.secret("shop-oidc"); string(s.Data["OIDC_CLIENT_SECRET"]) != za.secret || e.z.calls["RegenerateClientSecret"] != 1 {
		t.Fatalf("secret not rotated: %v", s.Data)
	}

	// Deleted Secret is recreated.
	if err := e.c.Delete(e.ctx, e.secret("shop-oidc")); err != nil {
		t.Fatal(err)
	}
	e.reconcile(e.app, ns, "shop-web")
	if s := e.secret("shop-oidc"); string(s.Data["OIDC_CLIENT_SECRET"]) != za.secret {
		t.Fatalf("secret not recreated: %v", s.Data)
	}

	// Roles: unmanaged roles are kept, removed managed roles are deleted.
	zp.roles["external"] = zitadel.Role{Key: "external", DisplayName: "external"}
	e.get(&proj, ns, "shop")
	proj.Spec.Roles = proj.Spec.Roles[:1]
	if err := e.c.Update(e.ctx, &proj); err != nil {
		t.Fatal(err)
	}
	e.reconcile(e.proj, ns, "shop")
	if _, ok := zp.roles["user"]; ok || len(zp.roles) != 2 {
		t.Fatalf("unexpected roles %+v", zp.roles)
	}

	// Deletion.
	e.get(&app, ns, "shop-web")
	if err := e.c.Delete(e.ctx, &app); err != nil {
		t.Fatal(err)
	}
	e.reconcile(e.app, ns, "shop-web")
	if len(zp.apps) != 0 {
		t.Fatal("application not deleted from Zitadel")
	}
	e.get(&proj, ns, "shop")
	if err := e.c.Delete(e.ctx, &proj); err != nil {
		t.Fatal(err)
	}
	e.reconcile(e.proj, ns, "shop")
	if len(e.z.projects) != 0 {
		t.Fatal("project not deleted from Zitadel")
	}
}

func TestAdoptionAfterStatusLoss(t *testing.T) {
	e := newEnv(t, newProject(), newOIDCApp())
	e.reconcile(e.proj, ns, "shop")
	e.reconcile(e.app, ns, "shop-web")
	var app zv1.ZitadelApplication
	e.get(&app, ns, "shop-web")
	appID := app.Status.AppID

	// Simulate a cluster rebuild: objects recreated without status / Secret.
	for _, name := range []string{"shop"} {
		var p zv1.ZitadelProject
		e.get(&p, ns, name)
		p.Status = zv1.ZitadelProjectStatus{}
		if err := e.c.Status().Update(e.ctx, &p); err != nil {
			t.Fatal(err)
		}
	}
	app.Status = zv1.ZitadelApplicationStatus{}
	if err := e.c.Status().Update(e.ctx, &app); err != nil {
		t.Fatal(err)
	}
	if err := e.c.Delete(e.ctx, e.secret("shop-oidc")); err != nil {
		t.Fatal(err)
	}

	e.reconcile(e.proj, ns, "shop")
	e.reconcile(e.app, ns, "shop-web")
	e.get(&app, ns, "shop-web")
	if app.Status.AppID != appID || e.z.calls["CreateProject"] != 1 || e.z.calls["CreateApp"] != 1 {
		t.Fatalf("expected adoption, got status %+v calls %v", app.Status, e.z.calls)
	}
	if e.z.calls["RegenerateClientSecret"] != 1 || len(e.secret("shop-oidc").Data["OIDC_CLIENT_SECRET"]) == 0 {
		t.Fatal("client secret should have been regenerated for the adopted application")
	}
}

func TestAPIApplicationWithKeyAndDefaultKeys(t *testing.T) {
	app := &zv1.ZitadelApplication{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-api", Namespace: ns},
		Spec: zv1.ZitadelApplicationSpec{
			ProjectRef: &zv1.LocalObjectReference{Name: "shop"},
			Type:       zv1.ApplicationTypeAPI,
		},
	}
	e := newEnv(t, newProject(), app)
	e.reconcile(e.proj, ns, "shop")
	e.reconcile(e.app, ns, "shop-api")
	e.get(app, ns, "shop-api")
	if c := ready(app.Status.Conditions); c.Status != metav1.ConditionTrue {
		t.Fatalf("app not ready: %+v", c)
	}
	s := e.secret("shop-api")
	for _, k := range []string{"CLIENT_ID", "ISSUER", "DISCOVERY_URL", "PROJECT_ID", "APP_ID", "ORGANIZATION_ID", "key.json"} {
		if len(s.Data[k]) == 0 {
			t.Fatalf("missing key %s in %v", k, s.Data)
		}
	}
	if _, ok := s.Data["CLIENT_SECRET"]; ok {
		t.Fatal("no client secret expected for PRIVATE_KEY_JWT")
	}
	firstKey := app.Status.KeyID

	app.Annotations = map[string]string{zv1.AnnotationRotateSecret: "now"}
	if err := e.c.Update(e.ctx, app); err != nil {
		t.Fatal(err)
	}
	e.reconcile(e.app, ns, "shop-api")
	e.get(app, ns, "shop-api")
	keys := e.z.projects[app.Status.ProjectID].apps[app.Status.AppID].keys
	if app.Status.KeyID == firstKey || len(keys) != 1 || !keys[app.Status.KeyID] {
		t.Fatalf("key not rotated: status %s keys %v", app.Status.KeyID, keys)
	}
}

func TestSecretConflictAndForbiddenNamespace(t *testing.T) {
	existing := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "shop-oidc", Namespace: ns}}
	forbidden := newProject()
	forbidden.Namespace = "other"
	e := newEnv(t, newProject(), newOIDCApp(), existing, forbidden)

	e.reconcile(e.proj, ns, "shop")
	if res := e.reconcile(e.app, ns, "shop-web"); res.RequeueAfter == 0 {
		t.Fatal("expected a delayed requeue")
	}
	var app zv1.ZitadelApplication
	e.get(&app, ns, "shop-web")
	if c := ready(app.Status.Conditions); c.Reason != ReasonSecretConflict {
		t.Fatalf("expected SecretConflict, got %+v", c)
	}
	if e.z.calls["RegenerateClientSecret"] != 0 {
		t.Fatal("client secret must not be rotated on conflict")
	}

	_, _ = e.proj.Reconcile(e.ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "other", Name: "shop"}})
	var proj zv1.ZitadelProject
	e.get(&proj, "other", "shop")
	if c := ready(proj.Status.Conditions); c.Reason != ReasonNamespaceForbidden {
		t.Fatalf("expected NamespaceForbidden, got %+v", c)
	}
}

func TestRenderSecretTemplateErrors(t *testing.T) {
	if err := validateTemplate(map[string]string{"x": "{{ .Nope }}"}); err == nil {
		t.Fatal("expected error for unknown field")
	}
	if err := validateTemplate(map[string]string{"x": "{{ .ClientID "}); err == nil {
		t.Fatal("expected parse error")
	}
}
