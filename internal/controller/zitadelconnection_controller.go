package controller

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
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

	err := r.reconcile(ctx, &conn)
	conn.Status.ObservedGeneration = conn.Generation
	result, err := resultFor(&conn.Status.Conditions, conn.Generation, err, "Connected to Zitadel")
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

func (r *ZitadelConnectionReconciler) reconcile(ctx context.Context, conn *zv1.ZitadelConnection) error {
	api, err := r.Clients.ForConnection(ctx, conn)
	if err != nil {
		return newError(ReasonConnectionError, err)
	}
	org, err := api.GetMyOrg(ctx, conn.Spec.OrganizationID)
	if err != nil {
		return newError(ReasonConnectionError, err)
	}
	conn.Status.OrganizationID = org.ID
	conn.Status.OrganizationName = org.Name
	return nil
}

// SetupWithManager registers the controller.
func (r *ZitadelConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zv1.ZitadelConnection{}).
		Named("zitadelconnection").
		Complete(r)
}
