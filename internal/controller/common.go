// Package controller contains the reconcilers of the zitadel-operator.
package controller

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
	"github.com/Apo-Z/zitadel-operator/internal/zitadel"
)

const (
	// ManagedByLabel is set on every Secret created by the operator. The
	// manager cache only watches Secrets carrying it.
	ManagedByLabel = "app.kubernetes.io/managed-by"
	// ManagedByValue is the value of ManagedByLabel.
	ManagedByValue = "zitadel-operator"

	// DefaultResyncPeriod is how often resources are re-checked against Zitadel
	// to correct drift.
	DefaultResyncPeriod = 10 * time.Minute
	waitRequeue         = 15 * time.Second
)

// Condition reasons.
const (
	ReasonReady              = "Ready"
	ReasonConnectionError    = "ConnectionError"
	ReasonNamespaceForbidden = "NamespaceForbidden"
	ReasonZitadelError       = "ZitadelError"
	ReasonProjectNotReady    = "ProjectNotReady"
	ReasonSecretConflict     = "SecretConflict"
	ReasonInvalidSpec        = "InvalidSpec"
)

// ClientFactory builds Zitadel API clients for a connection.
type ClientFactory interface {
	ForConnection(ctx context.Context, conn *zv1.ZitadelConnection) (zitadel.API, error)
}

// SecretClientFactory reads credentials from the referenced Secret and caches
// clients (and thus access tokens) until the connection or the Secret changes.
type SecretClientFactory struct {
	// Reader must be able to read any Secret (typically the uncached API reader).
	Reader client.Reader

	mu    sync.Mutex
	cache map[types.UID]cachedClient
}

type cachedClient struct {
	key string
	api zitadel.API
}

// ForConnection implements ClientFactory.
func (f *SecretClientFactory) ForConnection(ctx context.Context, conn *zv1.ZitadelConnection) (zitadel.API, error) {
	ref := conn.Spec.CredentialsSecretRef
	key := ref.Key
	if key == "" {
		key = "key.json"
	}
	var secret corev1.Secret
	if err := f.Reader.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, &secret); err != nil {
		return nil, fmt.Errorf("reading credentials secret %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	creds, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("credentials secret %s/%s has no key %q", ref.Namespace, ref.Name, key)
	}

	cacheKey := fmt.Sprintf("%d/%s", conn.Generation, secret.ResourceVersion)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cache == nil {
		f.cache = map[types.UID]cachedClient{}
	}
	if c, ok := f.cache[conn.UID]; ok && c.key == cacheKey {
		return c.api, nil
	}
	api, err := zitadel.NewClient(zitadel.Config{
		Issuer:                conn.Spec.URL,
		APIURL:                conn.Spec.InternalURL,
		Credentials:           creds,
		InsecureSkipTLSVerify: conn.Spec.InsecureSkipTLSVerify,
	})
	if err != nil {
		return nil, err
	}
	f.cache[conn.UID] = cachedClient{key: cacheKey, api: api}
	return api, nil
}

// reconcileError carries a condition reason and an optional requeue delay.
type reconcileError struct {
	reason       string
	err          error
	requeueAfter time.Duration
}

func (e *reconcileError) Error() string { return e.err.Error() }
func (e *reconcileError) Unwrap() error { return e.err }

func newError(reason string, err error) error {
	return &reconcileError{reason: reason, err: err}
}

func waitError(reason string, format string, args ...any) error {
	return &reconcileError{reason: reason, err: fmt.Errorf(format, args...), requeueAfter: waitRequeue}
}

// resultFor sets the Ready condition from err and computes the result.
func resultFor(conditions *[]metav1.Condition, generation int64, err error, readyMessage string) (ctrl.Result, error) {
	if err == nil {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:               zv1.ConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             ReasonReady,
			Message:            readyMessage,
			ObservedGeneration: generation,
		})
		return ctrl.Result{RequeueAfter: DefaultResyncPeriod}, nil
	}
	reason := ReasonZitadelError
	var rerr *reconcileError
	if errors.As(err, &rerr) {
		reason = rerr.reason
	}
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               zv1.ConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            truncate(err.Error(), 1024),
		ObservedGeneration: generation,
	})
	if rerr != nil && rerr.requeueAfter > 0 {
		return ctrl.Result{RequeueAfter: rerr.requeueAfter}, nil
	}
	switch reason {
	case ReasonNamespaceForbidden, ReasonInvalidSpec, ReasonSecretConflict:
		// Configuration errors: no exponential backoff, a spec change triggers a
		// new reconciliation anyway.
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	return ctrl.Result{}, err
}

// handleWriteError turns optimistic-locking conflicts and objects deleted in
// the meantime into a quiet requeue.
func handleWriteError(err error) (ctrl.Result, error) {
	if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return ctrl.Result{}, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// namespaceAllowed checks the allowedNamespaces of a connection.
func namespaceAllowed(conn *zv1.ZitadelConnection, namespace string) bool {
	if len(conn.Spec.AllowedNamespaces) == 0 {
		return true
	}
	for _, pattern := range conn.Spec.AllowedNamespaces {
		if ok, _ := path.Match(pattern, namespace); ok {
			return true
		}
	}
	return false
}

// getConnection fetches a connection and checks that namespace may use it.
func getConnection(ctx context.Context, c client.Client, name, namespace string) (*zv1.ZitadelConnection, error) {
	var conn zv1.ZitadelConnection
	if err := c.Get(ctx, types.NamespacedName{Name: name}, &conn); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, waitError(ReasonConnectionError, "ZitadelConnection %q not found", name)
		}
		return nil, err
	}
	if !namespaceAllowed(&conn, namespace) {
		return nil, newError(ReasonNamespaceForbidden,
			fmt.Errorf("namespace %q is not allowed to use ZitadelConnection %q", namespace, name))
	}
	return &conn, nil
}

// defaultOrganization returns the organization of a connection, or an error
// if the connection has not been reconciled yet.
func defaultOrganization(conn *zv1.ZitadelConnection) (string, error) {
	if conn.Spec.OrganizationID != "" {
		return conn.Spec.OrganizationID, nil
	}
	if conn.Status.OrganizationID != "" {
		return conn.Status.OrganizationID, nil
	}
	return "", waitError(ReasonConnectionError, "ZitadelConnection %q is not ready yet", conn.Name)
}

func issuerOf(conn *zv1.ZitadelConnection) string {
	return strings.TrimSuffix(conn.Spec.URL, "/")
}

// zitadelError wraps a Zitadel API error with a ZitadelError reason.
func zitadelError(action string, err error) error {
	return newError(ReasonZitadelError, fmt.Errorf("%s: %w", action, err))
}
