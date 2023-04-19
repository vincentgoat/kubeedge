package auth

import (
	corev1 "k8s.io/api/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/kubernetes/pkg/serviceaccount"
)

// clientGetter implements ServiceAccountTokenGetter using a clientset.Interface
type clientGetter struct {
	secretLister         corev1listers.SecretLister
	serviceAccountLister corev1listers.ServiceAccountLister
	podLister            corev1listers.PodLister
}

// NewGetterFromClient returns a ServiceAccountTokenGetter that
// uses the specified client to retrieve service accounts and secrets.
// The client should NOT authenticate using a service account token
// the returned getter will be used to retrieve, or recursion will result.
func NewGetterFromClient(secretLister corev1listers.SecretLister, serviceAccountLister corev1listers.ServiceAccountLister, podLister corev1listers.PodLister) serviceaccount.ServiceAccountTokenGetter {
	return clientGetter{secretLister, serviceAccountLister, podLister}
}

func (c clientGetter) GetServiceAccount(namespace, name string) (*corev1.ServiceAccount, error) {
	return c.serviceAccountLister.ServiceAccounts(namespace).Get(name)
}

func (c clientGetter) GetPod(namespace, name string) (*corev1.Pod, error) {
	return c.podLister.Pods(namespace).Get(name)
}

func (c clientGetter) GetSecret(namespace, name string) (*corev1.Secret, error) {
	return c.secretLister.Secrets(namespace).Get(name)
}
