package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

// ZitadelSMTPProviderReconciler reconciles ZitadelSMTPProvider objects.
type ZitadelSMTPProviderReconciler struct {
	client.Client
	// APIReader reads the referenced Secrets (not cached).
	APIReader client.Reader
	Clients   ClientFactory
}

// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelsmtpproviders,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelsmtpproviders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=zitadel.zitadel-operator.io,resources=zitadelsmtpproviders/finalizers,verbs=update

// Reconcile implements reconcile.Reconciler.
func (r *ZitadelSMTPProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var smtp zv1.ZitadelSMTPProvider
	if err := r.Get(ctx, req.NamespacedName, &smtp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !smtp.DeletionTimestamp.IsZero() {
		if err := r.finalize(ctx, &smtp); err != nil {
			return handleWriteError(err)
		}
		return ctrl.Result{}, nil
	}
	if controllerutil.AddFinalizer(&smtp, zv1.Finalizer) {
		if err := r.Update(ctx, &smtp); err != nil {
			return handleWriteError(err)
		}
	}

	err := r.reconcile(ctx, &smtp)
	if err == nil {
		smtp.Status.ObservedGeneration = smtp.Generation
	}
	result, err := resultFor(&smtp.Status.Conditions, smtp.Generation, err,
		fmt.Sprintf("SMTP provider %s is ready", smtp.Status.ProviderID))
	if uerr := r.Status().Update(ctx, &smtp); uerr != nil {
		return handleWriteError(uerr)
	}
	return result, err
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

func (r *ZitadelSMTPProviderReconciler) reconcile(ctx context.Context, smtp *zv1.ZitadelSMTPProvider) error {
	logger := log.FromContext(ctx)
	conn, err := getClusterConnection(ctx, r.Client, smtp.Spec.ConnectionRef.Name)
	if err != nil {
		return err
	}
	api, err := r.Clients.ForConnection(ctx, conn)
	if err != nil {
		return newError(ReasonConnectionError, err)
	}

	password, passwordHash := "", ""
	if ref := smtp.Spec.PasswordSecretRef; ref != nil {
		if password, err = readSecretKey(ctx, r.APIReader, ref.Namespace, ref.Name, ref.Key); err != nil {
			return err
		}
		passwordHash = hashOf(password)
	}

	desired := zitadel.SMTPConfig{
		Description:    smtp.Spec.Description,
		Host:           smtp.Spec.Host,
		User:           smtp.Spec.User,
		TLS:            boolOr(smtp.Spec.TLS, true),
		SenderAddress:  smtp.Spec.SenderAddress,
		SenderName:     smtp.Spec.SenderName,
		ReplyToAddress: smtp.Spec.ReplyToAddress,
	}
	if desired.Description == "" {
		desired.Description = smtp.Name
	}

	if smtp.Status.ConnectionName != "" && smtp.Status.ConnectionName != conn.Name {
		smtp.Status.ProviderID, smtp.Status.PasswordHash = "", ""
	}
	providers, err := api.ListEmailProviders(ctx)
	if err != nil {
		return zitadelError("listing email providers", err)
	}
	var current *zitadel.EmailProvider
	for _, match := range []func(zitadel.EmailProvider) bool{
		func(p zitadel.EmailProvider) bool { return p.ID == smtp.Status.ProviderID && p.ID != "" },
		func(p zitadel.EmailProvider) bool { return p.Description == desired.Description },
		func(p zitadel.EmailProvider) bool { return p.SMTP != nil && p.SMTP.Host == desired.Host },
	} {
		for i := range providers {
			if match(providers[i]) {
				current = &providers[i]
				break
			}
		}
		if current != nil {
			break
		}
	}

	switch {
	case current == nil:
		cfg := desired
		cfg.Password = password
		id, err := api.AddSMTPProvider(ctx, cfg)
		if err != nil {
			return zitadelError("adding SMTP provider", err)
		}
		logger.Info("created SMTP provider", "providerID", id)
		current = &zitadel.EmailProvider{ID: id}
	default:
		cur := zitadel.SMTPConfig{}
		if current.SMTP != nil {
			cur = *current.SMTP
		}
		cur.Description, cur.Password = current.Description, ""
		secretChanged := passwordHash != "" && passwordHash != smtp.Status.PasswordHash
		if cur != desired || secretChanged {
			cfg := desired
			if secretChanged {
				cfg.Password = password
			}
			if err := api.UpdateSMTPProvider(ctx, current.ID, cfg); err != nil {
				return zitadelError("updating SMTP provider", err)
			}
			logger.Info("updated SMTP provider", "providerID", current.ID, "passwordChanged", secretChanged)
		}
	}
	smtp.Status.ProviderID = current.ID
	smtp.Status.ConnectionName = conn.Name
	smtp.Status.PasswordHash = passwordHash

	if boolOr(smtp.Spec.Active, true) && current.State != zitadel.EmailProviderActive {
		if err := api.ActivateEmailProvider(ctx, current.ID); err != nil {
			return zitadelError("activating SMTP provider", err)
		}
		logger.Info("activated SMTP provider", "providerID", current.ID)
	}
	return nil
}

func (r *ZitadelSMTPProviderReconciler) finalize(ctx context.Context, smtp *zv1.ZitadelSMTPProvider) error {
	if !controllerutil.ContainsFinalizer(smtp, zv1.Finalizer) {
		return nil
	}
	if smtp.Spec.DeletionPolicy == zv1.DeletionPolicyDelete && smtp.Status.ProviderID != "" {
		var conn zv1.ZitadelConnection
		err := r.Get(ctx, types.NamespacedName{Name: smtp.Status.ConnectionName}, &conn)
		switch {
		case apierrors.IsNotFound(err):
			log.FromContext(ctx).Info("connection not found, cannot delete SMTP provider from Zitadel")
		case err != nil:
			return err
		default:
			api, err := r.Clients.ForConnection(ctx, &conn)
			if err != nil {
				return err
			}
			if err := api.DeleteEmailProvider(ctx, smtp.Status.ProviderID); err != nil && !zitadel.IsNotFound(err) {
				return fmt.Errorf("deleting SMTP provider %s: %w", smtp.Status.ProviderID, err)
			}
		}
	}
	controllerutil.RemoveFinalizer(smtp, zv1.Finalizer)
	return r.Update(ctx, smtp)
}

// SetupWithManager registers the controller.
func (r *ZitadelSMTPProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zv1.ZitadelSMTPProvider{}).
		Named("zitadelsmtpprovider").
		Complete(r)
}
