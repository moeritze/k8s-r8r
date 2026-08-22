package cluster

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/moeritze/k8s-r8r/internal/discovery"
)

func TestRBACScopeRules(t *testing.T) {
	tests := []struct {
		name  string
		scope RBACScope
		want  []rbacv1.PolicyRule
	}{
		{
			name:  "default scope",
			scope: DefaultRBACScope(),
			want: []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: replicaVerbs},
				{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: replicaVerbs},
				{APIGroups: []string{""}, Resources: []string{"namespaces"}, Verbs: []string{"get", "create", "patch"}},
			},
		},
		{
			name:  "narrowed to secrets only",
			scope: RBACScope{Resources: []ScopedResource{{Group: "", Resource: "secrets"}}},
			want: []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: replicaVerbs},
				{APIGroups: []string{""}, Resources: []string{"namespaces"}, Verbs: []string{"get", "create", "patch"}},
			},
		},
		{
			name:  "empty scope still allows namespace ensure only",
			scope: RBACScope{},
			want: []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"namespaces"}, Verbs: []string{"get", "create", "patch"}},
			},
		},
		{
			name:  "non-core group",
			scope: RBACScope{Resources: []ScopedResource{{Group: "apps", Resource: "deployments"}}},
			want: []rbacv1.PolicyRule{
				{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: replicaVerbs},
				{APIGroups: []string{""}, Resources: []string{"namespaces"}, Verbs: []string{"get", "create", "patch"}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.scope.Rules()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Rules =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

func TestBootstrapCreatesAllObjects(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewBootstrapperFromClient(cs)
	ctx := context.Background()

	if err := b.Bootstrap(ctx, DefaultRBACScope()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	if _, err := cs.CoreV1().Namespaces().Get(ctx, Namespace, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace missing: %v", err)
	}
	if _, err := cs.CoreV1().ServiceAccounts(Namespace).Get(ctx, ServiceAccountName, metav1.GetOptions{}); err != nil {
		t.Fatalf("serviceaccount missing: %v", err)
	}
	role, err := cs.RbacV1().ClusterRoles().Get(ctx, ClusterRoleName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("clusterrole missing: %v", err)
	}
	if !reflect.DeepEqual(role.Rules, DefaultRBACScope().Rules()) {
		t.Fatalf("clusterrole rules = %+v", role.Rules)
	}
	crb, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, ClusterRoleBindingName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("clusterrolebinding missing: %v", err)
	}
	if crb.Subjects[0].Name != ServiceAccountName || crb.Subjects[0].Namespace != Namespace {
		t.Fatalf("crb subject = %+v", crb.Subjects)
	}
	tokenRole, err := cs.RbacV1().Roles(Namespace).Get(ctx, TokenRoleName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("token role missing: %v", err)
	}
	rule := tokenRole.Rules[0]
	if rule.Resources[0] != "serviceaccounts/token" || rule.ResourceNames[0] != ServiceAccountName {
		t.Fatalf("token role rule = %+v", rule)
	}
	if _, err := cs.RbacV1().RoleBindings(Namespace).Get(ctx, TokenRoleBindingName, metav1.GetOptions{}); err != nil {
		t.Fatalf("token rolebinding missing: %v", err)
	}
}

func TestBootstrapIsIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewBootstrapperFromClient(cs)
	ctx := context.Background()

	if err := b.Bootstrap(ctx, DefaultRBACScope()); err != nil {
		t.Fatalf("first Bootstrap: %v", err)
	}
	if err := b.Bootstrap(ctx, DefaultRBACScope()); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
}

func TestUpdateRBACReNarrows(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewBootstrapperFromClient(cs)
	ctx := context.Background()

	if err := b.Bootstrap(ctx, DefaultRBACScope()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	narrow := RBACScope{Resources: []ScopedResource{{Group: "", Resource: "configmaps"}}}
	if err := b.UpdateRBAC(ctx, narrow); err != nil {
		t.Fatalf("UpdateRBAC: %v", err)
	}
	role, err := cs.RbacV1().ClusterRoles().Get(ctx, ClusterRoleName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("clusterrole: %v", err)
	}
	if !reflect.DeepEqual(role.Rules, narrow.Rules()) {
		t.Fatalf("re-narrowed rules = %+v, want %+v", role.Rules, narrow.Rules())
	}
}

func TestUpdateRBACCreatesWhenMissing(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewBootstrapperFromClient(cs)
	if err := b.UpdateRBAC(context.Background(), DefaultRBACScope()); err != nil {
		t.Fatalf("UpdateRBAC on empty cluster: %v", err)
	}
	if _, err := cs.RbacV1().ClusterRoles().Get(context.Background(), ClusterRoleName, metav1.GetOptions{}); err != nil {
		t.Fatalf("clusterrole missing: %v", err)
	}
}

const testKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://spoke.example:6443
  name: spoke
contexts:
- context:
    cluster: spoke
    user: admin
  name: spoke
current-context: spoke
users:
- name: admin
  user:
    token: super-secret-token
`

func TestRESTConfigFromKubeconfigSecret(t *testing.T) {
	tests := []struct {
		name    string
		data    map[string][]byte
		wantErr bool
	}{
		{name: "capi value key", data: map[string][]byte{"value": []byte(testKubeconfig)}},
		{name: "kubeconfig key", data: map[string][]byte{"kubeconfig": []byte(testKubeconfig)}},
		{name: "no recognized key", data: map[string][]byte{"other": []byte(testKubeconfig)}, wantErr: true},
		{name: "empty data", data: nil, wantErr: true},
		{name: "garbage kubeconfig", data: map[string][]byte{"value": []byte("{not yaml")}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "c1-kubeconfig"},
				Data:       tc.data,
			}
			cfg, err := RESTConfigFromKubeconfigSecret(secret)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if cfg.Host != "https://spoke.example:6443" {
				t.Fatalf("Host = %q", cfg.Host)
			}
		})
	}
}

// Compile-time check that CredentialRef stringification is what credentials
// error messages rely on.
var _ = discovery.CredentialRef{}
