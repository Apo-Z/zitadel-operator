package controller

import (
	"context"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

// ZitadelProjectReconciler reconciles ZitadelProject objects.
type ZitadelProjectReconciler struct {
	client.Client
	Clients ClientFactory
}

// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelprojects,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelprojects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelprojects/finalizers,verbs=update

// Reconcile implements reconcile.Reconciler.
func (r *ZitadelProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var proj zv1.ZitadelProject
	if err := r.Get(ctx, req.NamespacedName, &proj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !proj.DeletionTimestamp.IsZero() {
		if err := r.finalize(ctx, &proj); err != nil {
			return handleWriteError(err)
		}
		return ctrl.Result{}, nil
	}
	if controllerutil.AddFinalizer(&proj, zv1.Finalizer) {
		if err := r.Update(ctx, &proj); err != nil {
			return handleWriteError(err)
		}
	}

	err := r.reconcile(ctx, &proj)
	if err == nil {
		proj.Status.ObservedGeneration = proj.Generation
	}
	result, err := resultFor(&proj.Status.Conditions, proj.Generation, err,
		fmt.Sprintf("Project %s is ready", proj.Status.ProjectID))
	if uerr := r.Status().Update(ctx, &proj); uerr != nil {
		return handleWriteError(uerr)
	}
	return result, err
}

func projectSettings(proj *zv1.ZitadelProject) zitadel.ProjectSettings {
	name := proj.Spec.Name
	if name == "" {
		name = proj.Name
	}
	return zitadel.ProjectSettings{
		Name:                 name,
		ProjectRoleAssertion: proj.Spec.ProjectRoleAssertion,
		ProjectRoleCheck:     proj.Spec.ProjectRoleCheck,
		HasProjectCheck:      proj.Spec.HasProjectCheck,
	}
}

func (r *ZitadelProjectReconciler) reconcile(ctx context.Context, proj *zv1.ZitadelProject) error {
	logger := log.FromContext(ctx)

	conn, err := getConnection(ctx, r.Client, proj.Spec.ConnectionRef.Name, proj.Namespace)
	if err != nil {
		return err
	}
	orgID := proj.Spec.OrganizationID
	if orgID == "" {
		if orgID, err = defaultOrganization(conn); err != nil {
			return err
		}
	}
	api, err := r.Clients.ForConnection(ctx, conn)
	if err != nil {
		return newError(ReasonConnectionError, err)
	}

	// The project cannot move to another instance / organization: start over.
	if proj.Status.ProjectID != "" && (proj.Status.OrganizationID != orgID || proj.Status.ConnectionName != conn.Name) {
		logger.Info("connection or organization changed, a new project will be created",
			"oldProjectID", proj.Status.ProjectID)
		proj.Status.ProjectID = ""
		proj.Status.ManagedRoles = nil
	}

	desired := projectSettings(proj)
	var current *zitadel.Project
	if proj.Status.ProjectID != "" {
		current, err = api.GetProject(ctx, orgID, proj.Status.ProjectID)
		if zitadel.IsNotFound(err) {
			logger.Info("project disappeared from Zitadel, recreating it", "projectID", proj.Status.ProjectID)
			current, err = nil, nil
			proj.Status.ManagedRoles = nil
		}
		if err != nil {
			return zitadelError("getting project", err)
		}
	}
	if current == nil {
		current, err = api.FindProjectByName(ctx, orgID, desired.Name)
		if err != nil {
			return zitadelError("searching project", err)
		}
		if current != nil {
			logger.Info("adopting existing project", "projectID", current.ID)
		}
	}
	if current == nil {
		id, err := api.CreateProject(ctx, orgID, desired)
		if err != nil {
			return zitadelError("creating project", err)
		}
		logger.Info("created project", "projectID", id)
		current = &zitadel.Project{ID: id, Name: desired.Name,
			ProjectRoleAssertion: desired.ProjectRoleAssertion,
			ProjectRoleCheck:     desired.ProjectRoleCheck,
			HasProjectCheck:      desired.HasProjectCheck}
	}
	proj.Status.ProjectID = current.ID
	proj.Status.OrganizationID = orgID
	proj.Status.ConnectionName = conn.Name

	if current.Name != desired.Name ||
		current.ProjectRoleAssertion != desired.ProjectRoleAssertion ||
		current.ProjectRoleCheck != desired.ProjectRoleCheck ||
		current.HasProjectCheck != desired.HasProjectCheck {
		if err := api.UpdateProject(ctx, orgID, current.ID, desired); err != nil {
			return zitadelError("updating project", err)
		}
		logger.Info("updated project", "projectID", current.ID)
	}

	return r.reconcileRoles(ctx, api, proj, orgID)
}

func (r *ZitadelProjectReconciler) reconcileRoles(ctx context.Context, api zitadel.API, proj *zv1.ZitadelProject, orgID string) error {
	projectID := proj.Status.ProjectID
	existing, err := api.ListProjectRoles(ctx, orgID, projectID)
	if err != nil {
		return zitadelError("listing project roles", err)
	}
	byKey := make(map[string]zitadel.Role, len(existing))
	for _, role := range existing {
		byKey[role.Key] = role
	}

	desiredKeys := make([]string, 0, len(proj.Spec.Roles))
	for _, spec := range proj.Spec.Roles {
		role := zitadel.Role{Key: spec.Key, DisplayName: spec.DisplayName, Group: spec.Group}
		if role.DisplayName == "" {
			role.DisplayName = role.Key
		}
		desiredKeys = append(desiredKeys, role.Key)
		cur, ok := byKey[role.Key]
		switch {
		case !ok:
			if err := api.AddProjectRole(ctx, orgID, projectID, role); err != nil && !zitadel.IsAlreadyExists(err) {
				return zitadelError(fmt.Sprintf("adding role %q", role.Key), err)
			}
		case cur.DisplayName != role.DisplayName || cur.Group != role.Group:
			if err := api.UpdateProjectRole(ctx, orgID, projectID, role); err != nil {
				return zitadelError(fmt.Sprintf("updating role %q", role.Key), err)
			}
		}
	}

	// Only remove roles that this operator manages.
	for _, key := range proj.Status.ManagedRoles {
		if slices.Contains(desiredKeys, key) {
			continue
		}
		if _, ok := byKey[key]; !ok {
			continue
		}
		if err := api.RemoveProjectRole(ctx, orgID, projectID, key); err != nil && !zitadel.IsNotFound(err) {
			return zitadelError(fmt.Sprintf("removing role %q", key), err)
		}
	}
	slices.Sort(desiredKeys)
	proj.Status.ManagedRoles = desiredKeys
	return nil
}

func (r *ZitadelProjectReconciler) finalize(ctx context.Context, proj *zv1.ZitadelProject) error {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(proj, zv1.Finalizer) {
		return nil
	}
	if proj.Spec.DeletionPolicy != zv1.DeletionPolicyRetain && proj.Status.ProjectID != "" {
		connName := proj.Status.ConnectionName
		if connName == "" {
			connName = proj.Spec.ConnectionRef.Name
		}
		var conn zv1.ZitadelConnection
		err := r.Get(ctx, types.NamespacedName{Name: connName}, &conn)
		switch {
		case apierrors.IsNotFound(err):
			logger.Info("connection not found, cannot delete project from Zitadel", "connection", connName)
		case err != nil:
			return err
		default:
			api, err := r.Clients.ForConnection(ctx, &conn)
			if err != nil {
				return err
			}
			if err := api.DeleteProject(ctx, proj.Status.OrganizationID, proj.Status.ProjectID); err != nil && !zitadel.IsNotFound(err) {
				return fmt.Errorf("deleting project %s: %w", proj.Status.ProjectID, err)
			}
			logger.Info("deleted project from Zitadel", "projectID", proj.Status.ProjectID)
		}
	}
	controllerutil.RemoveFinalizer(proj, zv1.Finalizer)
	return r.Update(ctx, proj)
}

// SetupWithManager registers the controller.
func (r *ZitadelProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zv1.ZitadelProject{}).
		Named("zitadelproject").
		Complete(r)
}
