package v1alpha1

// ConnectionReference points to a cluster-scoped ZitadelConnection.
type ConnectionReference struct {
	// Name of the ZitadelConnection.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// LocalObjectReference references an object in the same namespace.
type LocalObjectReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// DeletionPolicy controls what happens to the Zitadel resource when the
// Kubernetes object is deleted.
// +kubebuilder:validation:Enum=Delete;Retain
type DeletionPolicy string

const (
	// DeletionPolicyDelete removes the resource from Zitadel.
	DeletionPolicyDelete DeletionPolicy = "Delete"
	// DeletionPolicyRetain leaves the resource in Zitadel.
	DeletionPolicyRetain DeletionPolicy = "Retain"
)

const (
	// ConditionReady is the main condition type of every resource of this API.
	ConditionReady = "Ready"

	// Finalizer added to resources that own something in Zitadel.
	Finalizer = "zitadel.zitadel-operator.io/finalizer"

	// AnnotationRotateSecret can be set (to any new value, e.g. a timestamp) on a
	// ZitadelApplication to force the rotation of its client secret / key.
	AnnotationRotateSecret = "zitadel.zitadel-operator.io/rotate-secret"
)

// SecretKeyReference selects a key of a Secret (any namespace: used by cluster-scoped resources).
type SecretKeyReference struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Namespace of the Secret.
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
	// Key in the Secret.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}
