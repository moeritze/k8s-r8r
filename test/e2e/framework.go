//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package e2e is the kind-fleet end-to-end suite (task group 10): a hub
// cluster running the operator (installed via the Helm chart) plus two spoke
// clusters, discovered through a simulated ClusterAPI inventory.
//
// # CAPI simulation
//
// The suite never installs ClusterAPI. The discovery provider reads Cluster
// objects unstructured, so testdata/capi-cluster-crd.yaml provides a minimal
// clusters.cluster.x-k8s.io CRD serving both v1beta2 (storage) and the
// deprecated v1beta1, so the provider's version negotiation is exercised
// rather than short-circuited; per spoke the suite creates a Cluster object
// labeled env=e2e, patches ControlPlaneReady=True through the status
// subresource, and writes the conventional "<name>-kubeconfig" Secret with an
// INTERNAL kind kubeconfig (kind get kubeconfig --internal) under the "value"
// key, so the operator pod reaches spoke API servers over the shared docker
// network.
//
// # Webhook
//
// The chart is installed with webhook.enabled=false: the webhook is advisory
// UX only (failurePolicy Ignore, policy is enforced authoritatively at
// reconcile time) and its envtest coverage exercises the admission path, so
// the e2e fleet skips the cert-manager dependency entirely.
package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

// Fleet naming (must match hack/kind-fleet.sh).
const (
	hubCluster = "r8r-hub"
	spoke1     = "r8r-spoke-1"
	spoke2     = "r8r-spoke-2"
)

// capiNamespace is the hub namespace holding the simulated ClusterAPI
// inventory (Cluster objects + kubeconfig Secrets).
const capiNamespace = "e2e-capi"

// e2eLabel marks every namespace and cluster-scoped object the suite creates,
// so re-runs can sweep leftovers.
const (
	e2eLabelKey   = "e2e.r8r.io/owned"
	e2eLabelValue = "true"
)

// operatorNamespace is where the chart installs the operator on the hub and
// where the bootstrap creates the spoke artifacts.
const operatorNamespace = "k8s-r8r-system"

// clusterGVK identifies simulated ClusterAPI Cluster objects. The stand-in
// CRD serves v1beta2 (storage) and the deprecated v1beta1, so the operator's
// discovery provider has a real negotiation to perform (issue #28) and must
// land on v1beta2; the suite writes at the storage version.
var clusterGVK = schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: "v1beta2", Kind: "Cluster"}

// Global harness state, initialized by TestMain.
var (
	repoRoot string
	scheme   = runtime.NewScheme()

	hubClient    client.Client
	spokeClients map[string]client.Client
	// kubeconfigPath maps cluster name to its host-facing kubeconfig file.
	kubeconfigPath map[string]string
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(r8rv1alpha1.AddToScheme(scheme))
}

// run executes a command from the repo root, returning combined output.
func run(env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// kubectl runs kubectl against the named fleet cluster.
func kubectl(clusterName string, args ...string) (string, error) {
	full := append([]string{"--kubeconfig", kubeconfigPath[clusterName]}, args...)
	return run(nil, "kubectl", full...)
}

// clientFor builds a controller-runtime client from a kubeconfig file.
func clientFor(path string) (client.Client, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig(raw)
	if err != nil {
		return nil, err
	}
	cfg.QPS = 50
	cfg.Burst = 100
	return client.New(cfg, client.Options{Scheme: scheme})
}

// restConfigFor builds a rest.Config from a fleet cluster's kubeconfig.
func restConfigFor(clusterName string) (*rest.Config, error) {
	raw, err := os.ReadFile(kubeconfigPath[clusterName])
	if err != nil {
		return nil, err
	}
	return clientcmd.RESTConfigFromKubeConfig(raw)
}

// ctx returns a fresh context for one API call chain.
func ctx() context.Context { return context.Background() }

// --- CAPI inventory simulation -------------------------------------------

// registerSpoke creates (or refreshes) the simulated ClusterAPI record for a
// spoke: the Cluster object with the given labels, ControlPlaneReady=True via
// the status subresource, and the "<name>-kubeconfig" Secret with the
// internal kind kubeconfig.
func registerSpoke(name string, labels map[string]string) error {
	internal, err := run(nil, "kind", "get", "kubeconfig", "--internal", "--name", name)
	if err != nil {
		return err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-kubeconfig",
			Namespace: capiNamespace,
			Labels:    map[string]string{e2eLabelKey: e2eLabelValue},
		},
		// "value" is the ClusterAPI kubeconfig-secret convention read by
		// internal/cluster/credentials.go.
		Data: map[string][]byte{"value": []byte(internal)},
	}
	if err := hubClient.Create(ctx(), secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		existing := &corev1.Secret{}
		if err := hubClient.Get(ctx(), client.ObjectKeyFromObject(secret), existing); err != nil {
			return err
		}
		existing.Data = secret.Data
		if err := hubClient.Update(ctx(), existing); err != nil {
			return err
		}
	}

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(clusterGVK)
	cluster.SetNamespace(capiNamespace)
	cluster.SetName(name)
	cluster.SetLabels(labels)
	if err := hubClient.Create(ctx(), cluster); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return setSpokeReady(name, true)
}

// setSpokeReady patches the Cluster object's ControlPlaneReady condition
// through the status subresource.
func setSpokeReady(name string, ready bool) error {
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(clusterGVK)
	if err := hubClient.Get(ctx(), types.NamespacedName{Namespace: capiNamespace, Name: name}, cluster); err != nil {
		return err
	}
	status := "True"
	if !ready {
		status = "False"
	}
	conditions := []any{map[string]any{
		"type":               "ControlPlaneReady",
		"status":             status,
		"reason":             "E2ESimulated",
		"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
	}}
	if err := unstructured.SetNestedSlice(cluster.Object, conditions, "status", "conditions"); err != nil {
		return err
	}
	return hubClient.Status().Update(ctx(), cluster)
}

// deregisterSpoke deletes the Cluster object (the kubeconfig Secret stays, as
// it would under ClusterAPI's deletion flow ordering).
func deregisterSpoke(name string) error {
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(clusterGVK)
	cluster.SetNamespace(capiNamespace)
	cluster.SetName(name)
	err := hubClient.Delete(ctx(), cluster)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// --- test object helpers --------------------------------------------------

// ensureNamespace creates (or resets the labels of) a suite-owned namespace.
func ensureNamespace(c client.Client, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   name,
		Labels: map[string]string{e2eLabelKey: e2eLabelValue},
	}}
	err := c.Create(ctx(), ns)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// policySpec bundles the knobs of a suite-created ReplicationPolicy.
type policySpec struct {
	name             string
	sourceNamespaces []string
	sourceKinds      []string
	targetNamespaces []string
	allowNSCreation  bool
	conflictPolicies []r8rv1alpha1.ConflictPolicy
	revocationPolicy r8rv1alpha1.RevocationPolicy
}

// applyPolicy creates or updates a ReplicationPolicy allowing env=e2e
// clusters for the given source/target dimensions.
func applyPolicy(p policySpec) error {
	desired := &r8rv1alpha1.ReplicationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:   p.name,
			Labels: map[string]string{e2eLabelKey: e2eLabelValue},
		},
		Spec: r8rv1alpha1.ReplicationPolicySpec{
			Sources: r8rv1alpha1.PolicySources{
				Namespaces: p.sourceNamespaces,
				Kinds:      p.sourceKinds,
			},
			Targets: r8rv1alpha1.PolicyTargets{
				ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{"env": "e2e"}},
				Namespaces:      p.targetNamespaces,
			},
			Options: r8rv1alpha1.PolicyOptions{
				AllowNamespaceCreation:  p.allowNSCreation,
				AllowedConflictPolicies: p.conflictPolicies,
				RevocationPolicy:        p.revocationPolicy,
			},
		},
	}
	existing := &r8rv1alpha1.ReplicationPolicy{}
	err := hubClient.Get(ctx(), types.NamespacedName{Name: p.name}, existing)
	if apierrors.IsNotFound(err) {
		return hubClient.Create(ctx(), desired)
	}
	if err != nil {
		return err
	}
	existing.Labels = desired.Labels
	existing.Spec = desired.Spec
	return hubClient.Update(ctx(), existing)
}

func deletePolicy(name string) error {
	pol := &r8rv1alpha1.ReplicationPolicy{ObjectMeta: metav1.ObjectMeta{Name: name}}
	err := hubClient.Delete(ctx(), pol)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// createAnnotatedSecret creates a hub Secret carrying a replication request.
// extraAnnotations may add r8r.io/target-namespaces, r8r.io/target-name, ...
func createAnnotatedSecret(namespace, name string, data map[string][]byte, extraAnnotations map[string]string) (*corev1.Secret, error) {
	ann := map[string]string{
		"r8r.io/replicate":       "true",
		"r8r.io/target-clusters": "env=e2e",
	}
	for k, v := range extraAnnotations {
		ann[k] = v
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: ann,
		},
		Data: data,
	}
	if err := hubClient.Create(ctx(), sec); err != nil {
		return nil, err
	}
	return sec, nil
}

// getReplication fetches the operator-materialized Replication for a source.
func getReplication(namespace, replicationName string) (*r8rv1alpha1.Replication, error) {
	rep := &r8rv1alpha1.Replication{}
	err := hubClient.Get(ctx(), types.NamespacedName{Namespace: namespace, Name: replicationName}, rep)
	return rep, err
}

// readyCondition returns the Ready condition of a Replication, or nil.
func readyCondition(rep *r8rv1alpha1.Replication) *metav1.Condition {
	return conditionOfType(rep, r8rv1alpha1.ReplicationConditionReady)
}

// conditionOfType returns the named condition of a Replication, or nil.
func conditionOfType(rep *r8rv1alpha1.Replication, condType string) *metav1.Condition {
	for i := range rep.Status.Conditions {
		if rep.Status.Conditions[i].Type == condType {
			return &rep.Status.Conditions[i]
		}
	}
	return nil
}

// replicasBySourceUID lists all replicas of a source (by source-uid label) on
// one spoke, across all namespaces.
func replicasBySourceUID(spoke client.Client, uid types.UID) (*corev1.SecretList, error) {
	list := &corev1.SecretList{}
	err := spoke.List(ctx(), list, client.MatchingLabels{
		"app.kubernetes.io/managed-by": "k8s-r8r",
		"r8r.io/source-uid":            string(uid),
	})
	return list, err
}

// spokeSecret reads one secret from a spoke.
func spokeSecret(spoke client.Client, namespace, name string) (*corev1.Secret, error) {
	sec := &corev1.Secret{}
	err := spoke.Get(ctx(), types.NamespacedName{Namespace: namespace, Name: name}, sec)
	return sec, err
}

// restoreSpokeClusterRoleBinding recreates the bootstrap ClusterRoleBinding
// on a spoke (used after tests that delete it to prove SA identity).
func restoreSpokeClusterRoleBinding(spoke client.Client) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "k8s-r8r-replicator",
			Labels: map[string]string{"app.kubernetes.io/managed-by": "k8s-r8r"},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "k8s-r8r-replicator",
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      "k8s-r8r",
			Namespace: operatorNamespace,
		}},
	}
	err := spoke.Create(ctx(), crb)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// deleteIgnoreNotFound deletes an object, treating NotFound as success.
func deleteIgnoreNotFound(c client.Client, obj client.Object) error {
	err := c.Delete(ctx(), obj)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// touchSource forces a source update (and thereby a reconcile) by bumping a
// data key.
func touchSource(namespace, name string) error {
	sec := &corev1.Secret{}
	if err := hubClient.Get(ctx(), types.NamespacedName{Namespace: namespace, Name: name}, sec); err != nil {
		return err
	}
	if sec.Data == nil {
		sec.Data = map[string][]byte{}
	}
	sec.Data["touched-at"] = []byte(time.Now().Format(time.RFC3339Nano))
	return hubClient.Update(ctx(), sec)
}

// deleteNamespaceAndWait deletes a namespace and waits until it is fully
// gone, force-removing r8r finalizers from stuck sources/Replications after a
// grace period (re-run robustness when a previous run died mid-flight).
func deleteNamespaceAndWait(c client.Client, name string, timeout time.Duration) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Delete(ctx(), ns); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	deadline := time.Now().Add(timeout)
	forced := false
	for time.Now().Before(deadline) {
		err := c.Get(ctx(), types.NamespacedName{Name: name}, &corev1.Namespace{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		// After half the timeout, force-drop operator finalizers so a
		// broken previous run cannot wedge namespace deletion forever.
		if !forced && time.Until(deadline) < timeout/2 {
			forced = true
			stripR8RFinalizers(c, name)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("namespace %s still terminating after %s", name, timeout)
}

// stripR8RFinalizers removes r8r.io finalizers from Secrets, ConfigMaps and
// Replications in a namespace (teardown escape hatch; docs/uninstall.md).
func stripR8RFinalizers(c client.Client, namespace string) {
	strip := func(obj client.Object) {
		var kept []string
		for _, f := range obj.GetFinalizers() {
			if !strings.HasPrefix(f, "r8r.io/") {
				kept = append(kept, f)
			}
		}
		if len(kept) != len(obj.GetFinalizers()) {
			obj.SetFinalizers(kept)
			_ = c.Update(ctx(), obj)
		}
	}
	secs := &corev1.SecretList{}
	if err := c.List(ctx(), secs, client.InNamespace(namespace)); err == nil {
		for i := range secs.Items {
			strip(&secs.Items[i])
		}
	}
	cms := &corev1.ConfigMapList{}
	if err := c.List(ctx(), cms, client.InNamespace(namespace)); err == nil {
		for i := range cms.Items {
			strip(&cms.Items[i])
		}
	}
	reps := &r8rv1alpha1.ReplicationList{}
	if err := c.List(ctx(), reps, client.InNamespace(namespace)); err == nil {
		for i := range reps.Items {
			strip(&reps.Items[i])
		}
	}
}

// eventsForObject lists hub events in a namespace whose involved object or
// reason matches.
func eventReasonsFor(namespace, objectName string) (map[string][]string, error) {
	list := &corev1.EventList{}
	if err := hubClient.List(ctx(), list, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, e := range list.Items {
		if e.InvolvedObject.Name == objectName {
			out[e.Reason] = append(out[e.Reason], e.Message)
		}
	}
	return out, nil
}

// logf writes a timestamped progress line usable from helpers.
func logf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("[%s] "+format, append([]any{time.Now().Format("15:04:05")}, args...)...)
}
