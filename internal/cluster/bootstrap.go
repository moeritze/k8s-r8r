// Package cluster manages per-target-cluster concerns: one-shot
// minimal-privilege credential bootstrap on spokes, short-lived ServiceAccount
// token rotation, and the runtime manager that keeps exactly one
// client/cache runtime per registered ready cluster.
//
// Security invariants (see design D5):
//   - The provider's admin kubeconfig is used exactly once per bootstrap
//     (and again only if the SA token hard-expires, which is a
//     re-bootstrap-class event). All steady-state traffic authenticates as
//     the dedicated k8s-r8r ServiceAccount.
//   - No secret data (kubeconfig bytes, tokens) ever appears in log or
//     error strings; only secret names/references are logged.
package cluster

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// Namespace is the dedicated namespace created on every spoke.
	Namespace = "k8s-r8r-system"
	// ServiceAccountName is the operator's identity on every spoke.
	ServiceAccountName = "k8s-r8r"
	// ClusterRoleName is the narrowly scoped replication ClusterRole.
	ClusterRoleName = "k8s-r8r-replicator"
	// ClusterRoleBindingName binds the ClusterRole to the ServiceAccount.
	ClusterRoleBindingName = "k8s-r8r-replicator"
	// TokenRoleName is the namespaced Role that lets the ServiceAccount
	// mint its own short-lived tokens (self-renewal without the admin
	// credential).
	TokenRoleName = "k8s-r8r-token"
	// TokenRoleBindingName binds TokenRoleName to the ServiceAccount.
	TokenRoleBindingName = "k8s-r8r-token"

	managedByLabelKey   = "app.kubernetes.io/managed-by"
	managedByLabelValue = "k8s-r8r"
)

// replicaVerbs are the verbs granted on every replicated resource kind.
var replicaVerbs = []string{"get", "list", "watch", "create", "update", "patch", "delete"}

// ScopedResource identifies one resource kind the policy universe allows
// replicating.
type ScopedResource struct {
	// Group is the API group ("" for core).
	Group string
	// Resource is the lowercase plural resource name (e.g. "secrets").
	Resource string
}

// RBACScope parameterizes the spoke ClusterRole by the resource kinds the
// current policy universe requires. Callers re-narrow by calling
// Bootstrapper.UpdateRBAC with a smaller scope when the policy universe
// shrinks.
//
// v1 limitation: scoping is via a ClusterRole, so granted verbs apply in all
// namespaces of the spoke (wildcard namespace scope). Narrowing to specific
// namespaces (per-namespace Roles) is a future refinement; the RBACScope
// type is the stable seam for it.
type RBACScope struct {
	// Resources are the replicated resource kinds.
	Resources []ScopedResource
}

// DefaultRBACScope covers the v1 kind allowlist: Secrets and ConfigMaps.
func DefaultRBACScope() RBACScope {
	return RBACScope{Resources: []ScopedResource{
		{Group: "", Resource: "secrets"},
		{Group: "", Resource: "configmaps"},
	}}
}

// Rules renders the scope into ClusterRole policy rules: full replica verbs
// on each scoped resource, plus get/create on namespaces (namespace-ensure;
// never delete).
func (s RBACScope) Rules() []rbacv1.PolicyRule {
	rules := make([]rbacv1.PolicyRule, 0, len(s.Resources)+1)
	for _, r := range s.Resources {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: []string{r.Group},
			Resources: []string{r.Resource},
			Verbs:     append([]string(nil), replicaVerbs...),
		})
	}
	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{""},
		Resources: []string{"namespaces"},
		Verbs:     []string{"get", "create"},
	})
	return rules
}

// Bootstrapper performs the one-shot, idempotent spoke bootstrap and RBAC
// re-narrowing against a single target cluster.
type Bootstrapper struct {
	client kubernetes.Interface
}

// NewBootstrapper builds a Bootstrapper from a rest config (typically the
// provider's admin kubeconfig, used only for bootstrap).
func NewBootstrapper(cfg *rest.Config) (*Bootstrapper, error) {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("cluster: building bootstrap client: %w", err)
	}
	return NewBootstrapperFromClient(cs), nil
}

// NewBootstrapperFromClient builds a Bootstrapper around an existing
// clientset (used by tests with a fake clientset).
func NewBootstrapperFromClient(cs kubernetes.Interface) *Bootstrapper {
	return &Bootstrapper{client: cs}
}

// Bootstrap idempotently ensures the namespace, ServiceAccount, narrow
// ClusterRole (+ binding) and the token-minting Role (+ binding) exist on the
// spoke. Safe to run repeatedly; reapplies the given RBAC scope.
func (b *Bootstrapper) Bootstrap(ctx context.Context, scope RBACScope) error {
	if err := b.ensureNamespace(ctx); err != nil {
		return err
	}
	if err := b.ensureServiceAccount(ctx); err != nil {
		return err
	}
	if err := b.UpdateRBAC(ctx, scope); err != nil {
		return err
	}
	if err := b.ensureClusterRoleBinding(ctx); err != nil {
		return err
	}
	if err := b.ensureTokenRole(ctx); err != nil {
		return err
	}
	return b.ensureTokenRoleBinding(ctx)
}

// UpdateRBAC creates or reapplies the ClusterRole rules for the given scope.
// Used both at bootstrap and to re-narrow when the policy universe shrinks.
func (b *Bootstrapper) UpdateRBAC(ctx context.Context, scope RBACScope) error {
	desired := scope.Rules()
	existing, err := b.client.RbacV1().ClusterRoles().Get(ctx, ClusterRoleName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		role := &rbacv1.ClusterRole{
			ObjectMeta: managedMeta(ClusterRoleName, ""),
			Rules:      desired,
		}
		if _, err := b.client.RbacV1().ClusterRoles().Create(ctx, role, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("cluster: creating ClusterRole %s: %w", ClusterRoleName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("cluster: reading ClusterRole %s: %w", ClusterRoleName, err)
	}
	existing.Rules = desired
	if _, err := b.client.RbacV1().ClusterRoles().Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("cluster: updating ClusterRole %s: %w", ClusterRoleName, err)
	}
	return nil
}

func (b *Bootstrapper) ensureNamespace(ctx context.Context) error {
	ns := &corev1.Namespace{ObjectMeta: managedMeta(Namespace, "")}
	_, err := b.client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("cluster: creating namespace %s: %w", Namespace, err)
	}
	return nil
}

func (b *Bootstrapper) ensureServiceAccount(ctx context.Context) error {
	sa := &corev1.ServiceAccount{ObjectMeta: managedMeta(ServiceAccountName, Namespace)}
	_, err := b.client.CoreV1().ServiceAccounts(Namespace).Create(ctx, sa, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("cluster: creating ServiceAccount %s/%s: %w", Namespace, ServiceAccountName, err)
	}
	return nil
}

func (b *Bootstrapper) ensureClusterRoleBinding(ctx context.Context) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: managedMeta(ClusterRoleBindingName, ""),
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     ClusterRoleName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      ServiceAccountName,
			Namespace: Namespace,
		}},
	}
	_, err := b.client.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("cluster: creating ClusterRoleBinding %s: %w", ClusterRoleBindingName, err)
	}
	return nil
}

// ensureTokenRole grants the ServiceAccount create on its own token
// subresource so rotation authenticates as the SA, not the admin credential.
func (b *Bootstrapper) ensureTokenRole(ctx context.Context) error {
	role := &rbacv1.Role{
		ObjectMeta: managedMeta(TokenRoleName, Namespace),
		Rules: []rbacv1.PolicyRule{{
			APIGroups:     []string{""},
			Resources:     []string{"serviceaccounts/token"},
			ResourceNames: []string{ServiceAccountName},
			Verbs:         []string{"create"},
		}},
	}
	_, err := b.client.RbacV1().Roles(Namespace).Create(ctx, role, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("cluster: creating Role %s/%s: %w", Namespace, TokenRoleName, err)
	}
	return nil
}

func (b *Bootstrapper) ensureTokenRoleBinding(ctx context.Context) error {
	rb := &rbacv1.RoleBinding{
		ObjectMeta: managedMeta(TokenRoleBindingName, Namespace),
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     TokenRoleName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      ServiceAccountName,
			Namespace: Namespace,
		}},
	}
	_, err := b.client.RbacV1().RoleBindings(Namespace).Create(ctx, rb, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("cluster: creating RoleBinding %s/%s: %w", Namespace, TokenRoleBindingName, err)
	}
	return nil
}

// managedMeta builds ObjectMeta with the managed-by label; namespace may be
// empty for cluster-scoped objects.
func managedMeta(name, namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		Labels:    map[string]string{managedByLabelKey: managedByLabelValue},
	}
}
