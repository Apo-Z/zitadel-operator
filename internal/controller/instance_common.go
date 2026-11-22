package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	zv1 "github.com/Apo-Z/zitadel-operator/api/v1alpha1"
)

// ReasonSecretNotFound is set while a referenced Secret (or key) is missing.
const ReasonSecretNotFound = "SecretNotFound"

// getClusterConnection fetches a connection for a cluster-scoped resource
// (allowedNamespaces does not apply: only cluster administrators create them).
func getClusterConnection(ctx context.Context, c client.Client, name string) (*zv1.ZitadelConnection, error) {
	var conn zv1.ZitadelConnection
	if err := c.Get(ctx, types.NamespacedName{Name: name}, &conn); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, waitError(ReasonConnectionError, "ZitadelConnection %q not found", name)
		}
		return nil, err
	}
	return &conn, nil
}

// readSecretKey reads a key of a Secret through the uncached reader.
func readSecretKey(ctx context.Context, r client.Reader, namespace, name, key string) (string, error) {
	var s corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &s); err != nil {
		if apierrors.IsNotFound(err) {
			return "", waitError(ReasonSecretNotFound, "Secret %s/%s not found", namespace, name)
		}
		return "", err
	}
	v, ok := s.Data[key]
	if !ok {
		return "", waitError(ReasonSecretNotFound, "Secret %s/%s has no key %q", namespace, name, key)
	}
	return string(v), nil
}

// hashOf returns a short hash identifying secret values (never the values themselves).
func hashOf(values ...string) string {
	h := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(h[:16])
}

// rawObject decodes a free-form object of the spec.
func rawObject(raw *runtime.RawExtension) (map[string]any, error) {
	if raw == nil || len(raw.Raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw.Raw, &m); err != nil {
		return nil, newError(ReasonInvalidSpec, fmt.Errorf("invalid object: %w", err))
	}
	return m, nil
}

// normalize renders a JSON value so that values returned by Zitadel compare
// equal to the desired ones (64-bit integers come back as strings, lists are
// compared without order).
func normalize(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return t
	case []any:
		items := make([]string, 0, len(t))
		for _, i := range t {
			items = append(items, normalize(i))
		}
		slices.Sort(items)
		return "[" + strings.Join(items, ",") + "]"
	case []string:
		items := slices.Clone(t)
		slices.Sort(items)
		return "[" + strings.Join(items, ",") + "]"
	case map[string]any:
		b, _ := json.Marshal(t)
		return string(b)
	}
	return fmt.Sprint(v)
}

// zeroLike returns the zero value of desired's JSON type (missing fields of a response).
func zeroLike(desired any) any {
	switch desired.(type) {
	case bool:
		return false
	case float64:
		return float64(0)
	case []any:
		return []any{}
	}
	return ""
}

// objectDiffers reports whether a field of desired differs from current.
func objectDiffers(desired, current map[string]any) bool {
	for k, want := range desired {
		got, ok := current[k]
		if !ok {
			got = zeroLike(want)
		}
		if normalize(want) != normalize(got) {
			return true
		}
	}
	return false
}

// sameSet reports whether two lists contain the same elements.
func sameSet(a, b []string) bool {
	return normalize(a) == normalize(b)
}
