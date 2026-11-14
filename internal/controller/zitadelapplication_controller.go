package controller

import (
	"context"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

// projectRefIndex indexes ZitadelApplications by spec.projectRef.name.
const projectRefIndex = ".spec.projectRef.name"

// ZitadelApplicationReconciler reconciles ZitadelApplication objects.
type ZitadelApplicationReconciler struct {
	client.Client
	// APIReader reads Secrets without the cache (the cache only contains
	// Secrets managed by the operator).
	APIReader client.Reader
	Clients   ClientFactory
}

// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelapplications,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelapplications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelapplications/finalizers,verbs=update
// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelprojects,verbs=get;list;watch

// Reconcile implements reconcile.Reconciler.
func (r *ZitadelApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var app zv1.ZitadelApplication
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !app.DeletionTimestamp.IsZero() {
		if err := r.finalize(ctx, &app); err != nil {
			return handleWriteError(err)
		}
		return ctrl.Result{}, nil
	}
	if controllerutil.AddFinalizer(&app, zv1.Finalizer) {
		if err := r.Update(ctx, &app); err != nil {
			return handleWriteError(err)
		}
	}

	err := r.reconcile(ctx, &app)
	if err == nil {
		app.Status.ObservedGeneration = app.Generation
	}
	result, err := resultFor(&app.Status.Conditions, app.Generation, err,
		fmt.Sprintf("Application %s is ready, credentials in Secret %s", app.Status.ClientID, app.Status.SecretName))
	if uerr := r.Status().Update(ctx, &app); uerr != nil {
		return handleWriteError(uerr)
	}
	return result, err
}

// target is where the application lives in Zitadel.
type target struct {
	conn      *zv1.ZitadelConnection
	orgID     string
	projectID string
}

func (r *ZitadelApplicationReconciler) resolveTarget(ctx context.Context, app *zv1.ZitadelApplication) (*target, error) {
	if app.Spec.ProjectRef != nil {
		var proj zv1.ZitadelProject
		key := types.NamespacedName{Namespace: app.Namespace, Name: app.Spec.ProjectRef.Name}
		if err := r.Get(ctx, key, &proj); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, waitError(ReasonProjectNotReady, "ZitadelProject %q not found", key.Name)
			}
			return nil, err
		}
		if !proj.DeletionTimestamp.IsZero() {
			return nil, waitError(ReasonProjectNotReady, "ZitadelProject %q is being deleted", key.Name)
		}
		if proj.Status.ProjectID == "" || !meta.IsStatusConditionTrue(proj.Status.Conditions, zv1.ConditionReady) {
			return nil, waitError(ReasonProjectNotReady, "ZitadelProject %q is not ready yet", key.Name)
		}
		conn, err := getConnection(ctx, r.Client, proj.Status.ConnectionName, app.Namespace)
		if err != nil {
			return nil, err
		}
		return &target{conn: conn, orgID: proj.Status.OrganizationID, projectID: proj.Status.ProjectID}, nil
	}

	if app.Spec.ConnectionRef == nil || app.Spec.ProjectID == "" {
		return nil, newError(ReasonInvalidSpec, fmt.Errorf("either projectRef or projectId+connectionRef must be set"))
	}
	conn, err := getConnection(ctx, r.Client, app.Spec.ConnectionRef.Name, app.Namespace)
	if err != nil {
		return nil, err
	}
	orgID := app.Spec.OrganizationID
	if orgID == "" {
		if orgID, err = defaultOrganization(conn); err != nil {
			return nil, err
		}
	}
	return &target{conn: conn, orgID: orgID, projectID: app.Spec.ProjectID}, nil
}

func appType(app *zv1.ZitadelApplication) zv1.ApplicationType {
	if app.Spec.Type == "" {
		return zv1.ApplicationTypeOIDC
	}
	return app.Spec.Type
}

func authMethod(app *zv1.ZitadelApplication) zv1.AuthMethod {
	if appType(app) == zv1.ApplicationTypeAPI {
		if app.Spec.API != nil && app.Spec.API.AuthMethod != "" {
			return app.Spec.API.AuthMethod
		}
		return zv1.AuthMethodPrivateKeyJWT
	}
	if app.Spec.OIDC != nil && app.Spec.OIDC.AuthMethod != "" {
		return app.Spec.OIDC.AuthMethod
	}
	return zv1.AuthMethodBasic
}

func (r *ZitadelApplicationReconciler) reconcile(ctx context.Context, app *zv1.ZitadelApplication) error {
	logger := log.FromContext(ctx)

	if err := validateTemplate(app.Spec.Secret.Template); err != nil {
		return newError(ReasonInvalidSpec, err)
	}
	if appType(app) == zv1.ApplicationTypeAPI && (authMethod(app) == zv1.AuthMethodPost || authMethod(app) == zv1.AuthMethodNone) {
		return newError(ReasonInvalidSpec, fmt.Errorf("API applications only support BASIC or PRIVATE_KEY_JWT"))
	}

	t, err := r.resolveTarget(ctx, app)
	if err != nil {
		return err
	}
	api, err := r.Clients.ForConnection(ctx, t.conn)
	if err != nil {
		return newError(ReasonConnectionError, err)
	}

	st := &app.Status
	if st.AppID != "" && (st.ProjectID != t.projectID || st.OrganizationID != t.orgID || st.ConnectionName != t.conn.Name) {
		logger.Info("application target changed, deleting the previous application", "appID", st.AppID)
		if app.Spec.DeletionPolicy != zv1.DeletionPolicyRetain {
			if err := api.DeleteApp(ctx, st.OrganizationID, st.ProjectID, st.AppID); err != nil && !zitadel.IsNotFound(err) {
				logger.Error(err, "could not delete previous application, ignoring")
			}
		}
		st.AppID, st.ClientID, st.KeyID = "", "", ""
	}
	st.ProjectID, st.OrganizationID, st.ConnectionName = t.projectID, t.orgID, t.conn.Name

	name := app.Spec.Name
	if name == "" {
		name = app.Name
	}
	isOIDC := appType(app) == zv1.ApplicationTypeOIDC
	method := authMethod(app)
	oidcCfg := desiredOIDCConfig(app.Spec.OIDC)
	apiCfg := zitadel.APIConfig{AuthMethodType: "API_AUTH_METHOD_TYPE_" + string(method)}

	var current *zitadel.App
	if st.AppID != "" {
		current, err = api.GetApp(ctx, t.orgID, t.projectID, st.AppID)
		if zitadel.IsNotFound(err) {
			logger.Info("application disappeared from Zitadel, recreating it", "appID", st.AppID)
			current, err = nil, nil
		}
		if err != nil {
			return zitadelError("getting application", err)
		}
	}
	if current == nil {
		current, err = api.FindAppByName(ctx, t.orgID, t.projectID, name)
		if err != nil {
			return zitadelError("searching application", err)
		}
		if current != nil {
			logger.Info("adopting existing application", "appID", current.ID)
		}
	}
	if current != nil && ((isOIDC && current.OIDCConfig == nil) || (!isOIDC && current.APIConfig == nil)) {
		logger.Info("application type changed, recreating it", "appID", current.ID)
		if err := api.DeleteApp(ctx, t.orgID, t.projectID, current.ID); err != nil && !zitadel.IsNotFound(err) {
			return zitadelError("deleting application", err)
		}
		current = nil
	}

	freshSecret := ""
	if current == nil {
		var creds *zitadel.AppCredentials
		if isOIDC {
			creds, err = api.CreateOIDCApp(ctx, t.orgID, t.projectID, name, oidcCfg)
		} else {
			creds, err = api.CreateAPIApp(ctx, t.orgID, t.projectID, name, apiCfg)
		}
		if err != nil {
			return zitadelError("creating application", err)
		}
		logger.Info("created application", "appID", creds.AppID, "clientID", creds.ClientID)
		st.AppID, st.ClientID, st.KeyID = creds.AppID, creds.ClientID, ""
		freshSecret = creds.ClientSecret
	} else {
		st.AppID, st.ClientID = current.ID, current.ClientID()
		if current.Name != name {
			if err := api.RenameApp(ctx, t.orgID, t.projectID, current.ID, name); err != nil {
				return zitadelError("renaming application", err)
			}
		}
		if isOIDC && !oidcConfigEqual(*current.OIDCConfig, oidcCfg) {
			if err := api.UpdateOIDCApp(ctx, t.orgID, t.projectID, current.ID, oidcCfg); err != nil {
				return zitadelError("updating OIDC configuration", err)
			}
			logger.Info("updated OIDC configuration", "appID", current.ID)
		}
		if !isOIDC && normalizeEnum(current.APIConfig.AuthMethodType, "API_AUTH_METHOD_TYPE_BASIC") != apiCfg.AuthMethodType {
			if err := api.UpdateAPIApp(ctx, t.orgID, t.projectID, current.ID, apiCfg); err != nil {
				return zitadelError("updating API configuration", err)
			}
			logger.Info("updated API configuration", "appID", current.ID)
		}
	}

	return r.reconcileSecret(ctx, api, app, t, name, method, freshSecret)
}

func (r *ZitadelApplicationReconciler) reconcileSecret(ctx context.Context, api zitadel.API, app *zv1.ZitadelApplication,
	t *target, appName string, method zv1.AuthMethod, freshSecret string) error {
	logger := log.FromContext(ctx)
	st := &app.Status

	secretName := app.Spec.Secret.Name
	if secretName == "" {
		secretName = app.Name
	}
	if st.SecretName != "" && st.SecretName != secretName {
		var old corev1.Secret
		err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: st.SecretName}, &old)
		if err == nil && metav1.IsControlledBy(&old, app) {
			if err := r.Delete(ctx, &old); client.IgnoreNotFound(err) != nil {
				return err
			}
		}
	}

	issuer := issuerOf(t.conn)
	data := SecretData{
		ClientID:       st.ClientID,
		Issuer:         issuer,
		DiscoveryURL:   issuer + "/.well-known/openid-configuration",
		ProjectID:      t.projectID,
		AppID:          st.AppID,
		OrganizationID: t.orgID,
		AppName:        appName,
	}
	hash := configHash(app.Spec.Secret.Template, data, string(method), app.Annotations[zv1.AnnotationRotateSecret])

	secret := &corev1.Secret{}
	err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: secretName}, secret)
	exists := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if exists && !metav1.IsControlledBy(secret, app) {
		return newError(ReasonSecretConflict,
			fmt.Errorf("secret %q already exists and is not owned by this ZitadelApplication", secretName))
	}
	if !exists {
		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: app.Namespace}}
	}

	oldKeyID := ""
	if !exists || freshSecret != "" || secret.Annotations[AnnotationConfigHash] != hash {
		switch method {
		case zv1.AuthMethodBasic, zv1.AuthMethodPost:
			data.ClientSecret = freshSecret
			if data.ClientSecret == "" {
				data.ClientSecret, err = api.RegenerateClientSecret(ctx, t.orgID, t.projectID, st.AppID, appType(app) == zv1.ApplicationTypeOIDC)
				if err != nil {
					return zitadelError("generating client secret", err)
				}
				logger.Info("generated a new client secret", "appID", st.AppID)
			}
		case zv1.AuthMethodPrivateKeyJWT:
			key, err := api.AddAppKey(ctx, t.orgID, t.projectID, st.AppID)
			if err != nil {
				return zitadelError("generating application key", err)
			}
			logger.Info("generated a new application key", "appID", st.AppID, "keyID", key.ID)
			data.KeyJSON = string(key.JSON)
			oldKeyID, st.KeyID = st.KeyID, key.ID
		}
		rendered, err := renderSecret(app.Spec.Secret.Template, data)
		if err != nil {
			return newError(ReasonInvalidSpec, err)
		}
		secret.Data = rendered
	}

	secret.Type = corev1.SecretTypeOpaque
	secret.Labels = map[string]string{}
	for k, v := range app.Spec.Secret.Labels {
		secret.Labels[k] = v
	}
	secret.Labels[ManagedByLabel] = ManagedByValue
	secret.Annotations = map[string]string{}
	for k, v := range app.Spec.Secret.Annotations {
		secret.Annotations[k] = v
	}
	secret.Annotations[AnnotationConfigHash] = hash
	if err := controllerutil.SetControllerReference(app, secret, r.Scheme()); err != nil {
		return err
	}
	if exists {
		err = r.Update(ctx, secret)
	} else {
		err = r.Create(ctx, secret)
	}
	if err != nil {
		return fmt.Errorf("writing secret %q: %w", secretName, err)
	}
	st.SecretName = secretName

	if oldKeyID != "" && oldKeyID != st.KeyID {
		if err := api.RemoveAppKey(ctx, t.orgID, t.projectID, st.AppID, oldKeyID); err != nil && !zitadel.IsNotFound(err) {
			logger.Error(err, "could not remove previous application key", "keyID", oldKeyID)
		}
	}
	return nil
}

func desiredOIDCConfig(spec *zv1.OIDCConfig) zitadel.OIDCConfig {
	if spec == nil {
		spec = &zv1.OIDCConfig{}
	}
	cfg := zitadel.OIDCConfig{
		RedirectURIs:             nonNil(spec.RedirectURIs),
		PostLogoutRedirectURIs:   nonNil(spec.PostLogoutRedirectURIs),
		AdditionalOrigins:        nonNil(spec.AdditionalOrigins),
		AppType:                  "OIDC_APP_TYPE_" + string(orDefault(spec.AppType, "WEB")),
		AuthMethodType:           "OIDC_AUTH_METHOD_TYPE_" + string(orDefault(spec.AuthMethod, zv1.AuthMethodBasic)),
		AccessTokenType:          "OIDC_TOKEN_TYPE_" + string(orDefault(spec.AccessTokenType, "BEARER")),
		Version:                  "OIDC_VERSION_1_0",
		DevMode:                  spec.DevMode,
		AccessTokenRoleAssertion: spec.AccessTokenRoleAssertion,
		IDTokenRoleAssertion:     spec.IDTokenRoleAssertion,
		IDTokenUserinfoAssertion: spec.IDTokenUserinfoAssertion,
		GrantTypes:               []string{},
		ResponseTypes:            []string{},
	}
	for _, g := range spec.GrantTypes {
		cfg.GrantTypes = append(cfg.GrantTypes, "OIDC_GRANT_TYPE_"+string(g))
	}
	if len(cfg.GrantTypes) == 0 {
		cfg.GrantTypes = []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE"}
	}
	for _, rt := range spec.ResponseTypes {
		cfg.ResponseTypes = append(cfg.ResponseTypes, "OIDC_RESPONSE_TYPE_"+string(rt))
	}
	if len(cfg.ResponseTypes) == 0 {
		cfg.ResponseTypes = []string{"OIDC_RESPONSE_TYPE_CODE"}
	}
	return cfg
}

// oidcConfigEqual compares the fields managed by the operator. Zero-valued
// enums may be omitted by the API and are normalized.
func oidcConfigEqual(current, desired zitadel.OIDCConfig) bool {
	norm := func(c zitadel.OIDCConfig) zitadel.OIDCConfig {
		c.ClientID = ""
		c.RedirectURIs = nonNil(c.RedirectURIs)
		c.PostLogoutRedirectURIs = nonNil(c.PostLogoutRedirectURIs)
		c.AdditionalOrigins = nonNil(c.AdditionalOrigins)
		c.GrantTypes = nonNil(c.GrantTypes)
		c.ResponseTypes = nonNil(c.ResponseTypes)
		c.AppType = normalizeEnum(c.AppType, "OIDC_APP_TYPE_WEB")
		c.AuthMethodType = normalizeEnum(c.AuthMethodType, "OIDC_AUTH_METHOD_TYPE_BASIC")
		c.AccessTokenType = normalizeEnum(c.AccessTokenType, "OIDC_TOKEN_TYPE_BEARER")
		c.Version = normalizeEnum(c.Version, "OIDC_VERSION_1_0")
		return c
	}
	return reflect.DeepEqual(norm(current), norm(desired))
}

func normalizeEnum(v, zero string) string {
	if v == "" {
		return zero
	}
	return v
}

func orDefault[T ~string](v, def T) T {
	if v == "" {
		return def
	}
	return v
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r *ZitadelApplicationReconciler) finalize(ctx context.Context, app *zv1.ZitadelApplication) error {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(app, zv1.Finalizer) {
		return nil
	}
	st := app.Status
	if app.Spec.DeletionPolicy != zv1.DeletionPolicyRetain && st.AppID != "" && st.ConnectionName != "" {
		var conn zv1.ZitadelConnection
		err := r.Get(ctx, types.NamespacedName{Name: st.ConnectionName}, &conn)
		switch {
		case apierrors.IsNotFound(err):
			logger.Info("connection not found, cannot delete application from Zitadel", "connection", st.ConnectionName)
		case err != nil:
			return err
		default:
			api, err := r.Clients.ForConnection(ctx, &conn)
			if err != nil {
				return err
			}
			if err := api.DeleteApp(ctx, st.OrganizationID, st.ProjectID, st.AppID); err != nil && !zitadel.IsNotFound(err) {
				return fmt.Errorf("deleting application %s: %w", st.AppID, err)
			}
			logger.Info("deleted application from Zitadel", "appID", st.AppID)
		}
	}
	controllerutil.RemoveFinalizer(app, zv1.Finalizer)
	return r.Update(ctx, app)
}

// SetupWithManager registers the controller.
func (r *ZitadelApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &zv1.ZitadelApplication{}, projectRefIndex,
		func(o client.Object) []string {
			app := o.(*zv1.ZitadelApplication)
			if app.Spec.ProjectRef == nil {
				return nil
			}
			return []string{app.Spec.ProjectRef.Name}
		}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&zv1.ZitadelApplication{}).
		Owns(&corev1.Secret{}).
		Watches(&zv1.ZitadelProject{}, handler.EnqueueRequestsFromMapFunc(r.appsForProject)).
		Named("zitadelapplication").
		Complete(r)
}

func (r *ZitadelApplicationReconciler) appsForProject(ctx context.Context, o client.Object) []reconcile.Request {
	var apps zv1.ZitadelApplicationList
	if err := r.List(ctx, &apps, client.InNamespace(o.GetNamespace()),
		client.MatchingFields{projectRefIndex: o.GetName()}); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(apps.Items))
	for _, app := range apps.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&app)})
	}
	return reqs
}
