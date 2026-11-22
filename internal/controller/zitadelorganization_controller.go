package controller

import (
	"context"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

// ZitadelOrganizationReconciler reconciles ZitadelOrganization objects.
type ZitadelOrganizationReconciler struct {
	client.Client
	// APIReader reads the referenced Secrets (not cached).
	APIReader client.Reader
	Clients   ClientFactory
}

// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelorganizations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelorganizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelorganizations/finalizers,verbs=update

// Reconcile implements reconcile.Reconciler.
func (r *ZitadelOrganizationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var org zv1.ZitadelOrganization
	if err := r.Get(ctx, req.NamespacedName, &org); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !org.DeletionTimestamp.IsZero() {
		if err := r.finalize(ctx, &org); err != nil {
			return handleWriteError(err)
		}
		return ctrl.Result{}, nil
	}
	if controllerutil.AddFinalizer(&org, zv1.Finalizer) {
		if err := r.Update(ctx, &org); err != nil {
			return handleWriteError(err)
		}
	}

	err := r.reconcile(ctx, &org)
	if err == nil {
		org.Status.ObservedGeneration = org.Generation
	}
	result, err := resultFor(&org.Status.Conditions, org.Generation, err,
		fmt.Sprintf("Organization %s is ready", org.Status.OrganizationID))
	if uerr := r.Status().Update(ctx, &org); uerr != nil {
		return handleWriteError(uerr)
	}
	return result, err
}

func (r *ZitadelOrganizationReconciler) reconcile(ctx context.Context, org *zv1.ZitadelOrganization) error {
	conn, err := getClusterConnection(ctx, r.Client, org.Spec.ConnectionRef.Name)
	if err != nil {
		return err
	}
	api, err := r.Clients.ForConnection(ctx, conn)
	if err != nil {
		return newError(ReasonConnectionError, err)
	}
	if err := r.reconcileOrg(ctx, api, org, conn.Name); err != nil {
		return err
	}
	orgID := org.Status.OrganizationID
	if err := r.reconcilePolicies(ctx, api, org, orgID); err != nil {
		return err
	}
	if err := r.reconcileIDPs(ctx, api, org, orgID); err != nil {
		return err
	}
	return r.reconcileActions(ctx, api, org, orgID)
}

func (r *ZitadelOrganizationReconciler) reconcileOrg(ctx context.Context, api zitadel.API, org *zv1.ZitadelOrganization, connName string) error {
	logger := log.FromContext(ctx)
	name := org.Spec.Name
	if name == "" {
		name = org.Name
	}
	if org.Status.ConnectionName != "" && org.Status.ConnectionName != connName {
		org.Status = zv1.ZitadelOrganizationStatus{Conditions: org.Status.Conditions}
	}
	orgs, err := api.ListOrganizations(ctx)
	if err != nil {
		return zitadelError("listing organizations", err)
	}
	id := ""
	for _, o := range orgs {
		if org.Status.OrganizationID != "" && o.ID == org.Status.OrganizationID {
			id = o.ID
			break
		}
	}
	if id == "" {
		for _, o := range orgs {
			if o.Name == name {
				id = o.ID
				logger.Info("adopting existing organization", "organizationID", id)
				break
			}
		}
	}
	if id == "" {
		if id, err = api.CreateOrganization(ctx, name); err != nil {
			return zitadelError("creating organization", err)
		}
		logger.Info("created organization", "organizationID", id)
	}
	org.Status.OrganizationID = id
	org.Status.ConnectionName = connName

	if org.Spec.Default {
		current, err := api.GetDefaultOrganization(ctx)
		if err != nil {
			return zitadelError("reading default organization", err)
		}
		if current != id {
			if err := api.SetDefaultOrganization(ctx, id); err != nil {
				return zitadelError("setting default organization", err)
			}
			logger.Info("set default organization", "organizationID", id)
		}
	}
	return nil
}

func (r *ZitadelOrganizationReconciler) reconcilePolicies(ctx context.Context, api zitadel.API, org *zv1.ZitadelOrganization, orgID string) error {
	for _, p := range []struct {
		kind zitadel.PolicyKind
		raw  *runtime.RawExtension
	}{
		{zitadel.PolicyPasswordComplexity, org.Spec.PasswordComplexityPolicy},
		{zitadel.PolicyLockout, org.Spec.LockoutPolicy},
		{zitadel.PolicyLogin, org.Spec.LoginPolicy},
	} {
		desired, err := rawObject(p.raw)
		if err != nil {
			return err
		}
		if desired == nil {
			continue
		}
		current, isDefault, err := api.GetPolicy(ctx, orgID, p.kind)
		if err != nil {
			return zitadelError(fmt.Sprintf("reading %s policy", p.kind), err)
		}
		if !isDefault && !objectDiffers(desired, current) {
			continue
		}
		if err := api.SetPolicy(ctx, orgID, p.kind, desired, isDefault); err != nil {
			return zitadelError(fmt.Sprintf("setting %s policy", p.kind), err)
		}
		log.FromContext(ctx).Info("set policy", "policy", p.kind)
	}
	return nil
}

func (r *ZitadelOrganizationReconciler) reconcileIDPs(ctx context.Context, api zitadel.API, org *zv1.ZitadelOrganization, orgID string) error {
	logger := log.FromContext(ctx)
	existing, err := api.ListIDPs(ctx, orgID)
	if err != nil {
		return zitadelError("listing identity providers", err)
	}
	byName := map[string]zitadel.IDP{}
	for _, idp := range existing {
		byName[idp.Name] = idp
	}
	hashes := map[string]string{}
	names := make([]string, 0, len(org.Spec.IdentityProviders))
	for _, spec := range org.Spec.IdentityProviders {
		names = append(names, spec.Name)
		ref := spec.CredentialsSecretRef
		idKey, secretKey := ref.ClientIDKey, ref.ClientSecretKey
		if idKey == "" {
			idKey = "CLIENT_ID"
		}
		if secretKey == "" {
			secretKey = "CLIENT_SECRET"
		}
		clientID, err := readSecretKey(ctx, r.APIReader, ref.Namespace, ref.Name, idKey)
		if err != nil {
			return err
		}
		clientSecret, err := readSecretKey(ctx, r.APIReader, ref.Namespace, ref.Name, secretKey)
		if err != nil {
			return err
		}
		options, err := rawObject(spec.Options)
		if err != nil {
			return err
		}
		if options == nil {
			options = map[string]any{}
		}
		desired := zitadel.GoogleIDP{Name: spec.Name, ClientID: clientID, Scopes: spec.Scopes, ProviderOptions: options}
		if desired.Scopes == nil {
			desired.Scopes = []string{}
		}
		hash := hashOf(clientID, clientSecret)
		hashes[spec.Name] = hash

		cur, ok := byName[spec.Name]
		switch {
		case !ok:
			desired.ClientSecret = clientSecret
			if _, err := api.AddGoogleIDP(ctx, orgID, desired); err != nil {
				return zitadelError(fmt.Sprintf("adding identity provider %q", spec.Name), err)
			}
			logger.Info("created identity provider", "name", spec.Name)
		default:
			if cur.Config.Google == nil {
				return newError(ReasonInvalidSpec, fmt.Errorf("identity provider %q exists with another type", spec.Name))
			}
			secretChanged := hash != org.Status.CredentialsHashes[spec.Name]
			if secretChanged || cur.Config.Google.ClientID != clientID ||
				!sameSet(cur.Config.Google.Scopes, desired.Scopes) || objectDiffers(options, cur.Config.Options) {
				if secretChanged {
					desired.ClientSecret = clientSecret
				}
				if err := api.UpdateGoogleIDP(ctx, orgID, cur.ID, desired); err != nil {
					return zitadelError(fmt.Sprintf("updating identity provider %q", spec.Name), err)
				}
				logger.Info("updated identity provider", "name", spec.Name, "credentialsChanged", secretChanged)
			}
		}
	}
	for _, name := range org.Status.ManagedIdentityProviders {
		cur, ok := byName[name]
		if slices.Contains(names, name) || !ok {
			continue
		}
		if err := api.DeleteIDP(ctx, orgID, cur.ID); err != nil && !zitadel.IsNotFound(err) {
			return zitadelError(fmt.Sprintf("deleting identity provider %q", name), err)
		}
		logger.Info("deleted identity provider", "name", name)
	}
	slices.Sort(names)
	org.Status.ManagedIdentityProviders = names
	org.Status.CredentialsHashes = hashes
	if len(hashes) == 0 {
		org.Status.CredentialsHashes = nil
	}
	return nil
}

func (r *ZitadelOrganizationReconciler) reconcileActions(ctx context.Context, api zitadel.API, org *zv1.ZitadelOrganization, orgID string) error {
	logger := log.FromContext(ctx)
	existing, err := api.ListActions(ctx, orgID)
	if err != nil {
		return zitadelError("listing actions", err)
	}
	byName := map[string]zitadel.Action{}
	for _, a := range existing {
		byName[a.Name] = a
	}
	ids := map[string]string{} // action name -> ID
	names := make([]string, 0, len(org.Spec.Actions))
	for _, spec := range org.Spec.Actions {
		names = append(names, spec.Name)
		desired := zitadel.Action{Name: spec.Name, Script: spec.Script, Timeout: spec.Timeout, AllowedToFail: spec.AllowedToFail}
		if desired.Timeout == "" {
			desired.Timeout = "10s"
		}
		cur, ok := byName[spec.Name]
		switch {
		case !ok:
			id, err := api.CreateAction(ctx, orgID, desired)
			if err != nil {
				return zitadelError(fmt.Sprintf("creating action %q", spec.Name), err)
			}
			ids[spec.Name] = id
			logger.Info("created action", "name", spec.Name)
		default:
			ids[spec.Name] = cur.ID
			if cur.Script != desired.Script || cur.Timeout != desired.Timeout || cur.AllowedToFail != desired.AllowedToFail {
				if err := api.UpdateAction(ctx, orgID, cur.ID, desired); err != nil {
					return zitadelError(fmt.Sprintf("updating action %q", spec.Name), err)
				}
				logger.Info("updated action", "name", spec.Name)
			}
		}
	}
	for _, name := range org.Status.ManagedActions {
		cur, ok := byName[name]
		if slices.Contains(names, name) || !ok {
			continue
		}
		if err := api.DeleteAction(ctx, orgID, cur.ID); err != nil && !zitadel.IsNotFound(err) {
			return zitadelError(fmt.Sprintf("deleting action %q", name), err)
		}
		logger.Info("deleted action", "name", name)
	}

	// Triggers: on each flow used by the spec, the managed actions are exactly the
	// desired ones; actions not managed by this object are kept.
	managed := map[string]bool{}
	for _, id := range ids {
		managed[id] = true
	}
	desiredByFlow := map[string]map[string][]string{} // flow -> trigger -> action IDs
	for _, spec := range org.Spec.Actions {
		for _, t := range spec.Triggers {
			if desiredByFlow[t.FlowType] == nil {
				desiredByFlow[t.FlowType] = map[string][]string{}
			}
			for _, tt := range t.TriggerTypes {
				desiredByFlow[t.FlowType][tt] = append(desiredByFlow[t.FlowType][tt], ids[spec.Name])
			}
		}
	}
	flows := make([]string, 0, len(desiredByFlow))
	for f := range desiredByFlow {
		flows = append(flows, f)
	}
	slices.Sort(flows)
	for _, flow := range flows {
		current, err := api.GetFlowTriggers(ctx, orgID, flow)
		if err != nil {
			return zitadelError(fmt.Sprintf("reading flow %s", flow), err)
		}
		triggers := map[string]bool{}
		for t := range current {
			triggers[t] = true
		}
		for t := range desiredByFlow[flow] {
			triggers[t] = true
		}
		for trigger := range triggers {
			want := []string{}
			for _, id := range current[trigger] {
				if !managed[id] {
					want = append(want, id)
				}
			}
			want = append(want, desiredByFlow[flow][trigger]...)
			if sameSet(want, current[trigger]) {
				continue
			}
			if err := api.SetTriggerActions(ctx, orgID, flow, trigger, want); err != nil {
				return zitadelError(fmt.Sprintf("setting actions of flow %s trigger %s", flow, trigger), err)
			}
			logger.Info("set trigger actions", "flow", flow, "trigger", trigger)
		}
	}
	slices.Sort(names)
	org.Status.ManagedActions = names
	return nil
}

func (r *ZitadelOrganizationReconciler) finalize(ctx context.Context, org *zv1.ZitadelOrganization) error {
	if !controllerutil.ContainsFinalizer(org, zv1.Finalizer) {
		return nil
	}
	if org.Spec.DeletionPolicy == zv1.DeletionPolicyDelete && org.Status.OrganizationID != "" {
		var conn zv1.ZitadelConnection
		err := r.Get(ctx, types.NamespacedName{Name: org.Status.ConnectionName}, &conn)
		switch {
		case apierrors.IsNotFound(err):
			log.FromContext(ctx).Info("connection not found, cannot delete organization from Zitadel")
		case err != nil:
			return err
		default:
			api, err := r.Clients.ForConnection(ctx, &conn)
			if err != nil {
				return err
			}
			if err := api.DeleteOrganization(ctx, org.Status.OrganizationID); err != nil && !zitadel.IsNotFound(err) {
				return fmt.Errorf("deleting organization %s: %w", org.Status.OrganizationID, err)
			}
		}
	}
	controllerutil.RemoveFinalizer(org, zv1.Finalizer)
	return r.Update(ctx, org)
}

// SetupWithManager registers the controller.
func (r *ZitadelOrganizationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zv1.ZitadelOrganization{}).
		Named("zitadelorganization").
		Complete(r)
}
