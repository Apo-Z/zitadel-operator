package controller

import (
	"context"
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

// ZitadelConnectionReconciler checks connectivity to a Zitadel instance.
type ZitadelConnectionReconciler struct {
	client.Client
	Clients ClientFactory
}

// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile implements reconcile.Reconciler.
func (r *ZitadelConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var conn zv1.ZitadelConnection
	if err := r.Get(ctx, req.NamespacedName, &conn); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	limited, err := r.reconcile(ctx, &conn)
	conn.Status.ObservedGeneration = conn.Generation
	message := "Connected to Zitadel"
	if limited {
		message = "Connected to Zitadel without org.read permission: only projects where the service account is PROJECT_OWNER can be managed"
	}
	result, err := resultFor(&conn.Status.Conditions, conn.Generation, err, message)
	if err != nil {
		// Retry connection errors with a bounded delay rather than hot-looping.
		result, err = ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	} else if result.RequeueAfter > 5*time.Minute {
		result.RequeueAfter = 5 * time.Minute
	}
	if uerr := r.Status().Update(ctx, &conn); uerr != nil {
		return handleWriteError(uerr)
	}
	return result, err
}

// reconcile checks the connection. limited is true when the service account
// cannot read its organization (e.g. it only has PROJECT_OWNER memberships).
func (r *ZitadelConnectionReconciler) reconcile(ctx context.Context, conn *zv1.ZitadelConnection) (limited bool, err error) {
	api, err := r.Clients.ForConnection(ctx, conn)
	if err != nil {
		return false, newError(ReasonConnectionError, err)
	}
	org, err := api.GetMyOrg(ctx, conn.Spec.OrganizationID)
	if err == nil {
		conn.Status.OrganizationID = org.ID
		conn.Status.OrganizationName = org.Name
		return false, nil
	}
	if !zitadel.IsPermissionDenied(err) {
		return false, newError(ReasonConnectionError, err)
	}

	// No org.read permission: the auth API still tells us who we are.
	orgID, uerr := api.GetMyUserOrg(ctx)
	if uerr != nil {
		return false, newError(ReasonConnectionError, fmt.Errorf("%w (fallback on /auth/v1/users/me: %v)", err, uerr))
	}
	if conn.Spec.OrganizationID != "" {
		orgID = conn.Spec.OrganizationID
	}
	conn.Status.OrganizationID = orgID
	conn.Status.OrganizationName = ""
	return true, nil
}

// SetupWithManager registers the controller.
func (r *ZitadelConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zv1.ZitadelConnection{}).
		Named("zitadelconnection").
		Complete(r)
}
