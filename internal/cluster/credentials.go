package cluster

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/moritzroeseler/k8s-r8r/internal/discovery"
)

// kubeconfigSecretKeys are the data keys tried, in order, when extracting a
// kubeconfig from a credential Secret. "value" is the ClusterAPI convention.
var kubeconfigSecretKeys = []string{"value", "kubeconfig"}

// LoadAdminConfig reads the credential Secret referenced by ref from the hub
// and parses the kubeconfig into a rest config. The returned config is the
// provider's admin credential: use it for one-shot bootstrap only.
//
// Errors reference the Secret by name only; kubeconfig bytes never appear in
// error strings.
func LoadAdminConfig(ctx context.Context, reader client.Reader, ref discovery.CredentialRef) (*rest.Config, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
	if err := reader.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("cluster: reading credential secret %s: %w", ref, err)
	}
	return RESTConfigFromKubeconfigSecret(secret)
}

// RESTConfigFromKubeconfigSecret extracts and parses the kubeconfig held in a
// credential Secret.
func RESTConfigFromKubeconfigSecret(secret *corev1.Secret) (*rest.Config, error) {
	ref := discovery.CredentialRef{Namespace: secret.Namespace, Name: secret.Name}
	for _, k := range kubeconfigSecretKeys {
		data, ok := secret.Data[k]
		if !ok || len(data) == 0 {
			continue
		}
		cfg, err := clientcmd.RESTConfigFromKubeConfig(data)
		if err != nil {
			// Deliberately do not wrap parser output details beyond
			// the error itself; never include the kubeconfig bytes.
			return nil, fmt.Errorf("cluster: parsing kubeconfig from secret %s key %q: %w", ref, k, err)
		}
		return cfg, nil
	}
	return nil, fmt.Errorf("cluster: credential secret %s has no kubeconfig under keys %v", ref, kubeconfigSecretKeys)
}
