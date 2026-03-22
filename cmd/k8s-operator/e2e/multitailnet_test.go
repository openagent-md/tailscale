// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package e2e

import (
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tsapi "tailscale.com/k8s-operator/apis/v1alpha1"
	"tailscale.com/tstest"
)

// TestMultiTailnet verifies that ProxyGroup resources are created in the correct Tailnet,
// and that an Ingress resource has its Tailscale Service created in the correct Tailnet.
func TestMultiTailnet(t *testing.T) {
	if tnClient == nil || secondTSClient == nil {
		t.Skip("TestMultiTailnet requires a working tailnet client for a primary and second tailnet")
	}

	t.Log(secondClientID)
	t.Log(secondClientSecret)

	// Create the tailnet Secret in the tailscale namespace.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "second-tailnet-credentials",
			Namespace: "tailscale",
		},
		Data: map[string][]byte{
			"client_id":     []byte(secondClientID),
			"client_secret": []byte(secondClientSecret),
		},
	}
	createAndCleanup(t, kubeClient, secret)

	// Create the Tailnet resource.
	tn := &tsapi.Tailnet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "second-tailnet",
		},
		Spec: tsapi.TailnetSpec{
			LoginURL: clusterLoginServer,
			Credentials: tsapi.TailnetCredentials{
				SecretName: "second-tailnet-credentials",
			},
		},
	}
	createAndCleanup(t, kubeClient, tn)

	// Apply nginx Deployment and Service.
	createAndCleanup(t, kubeClient, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/name": "nginx",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: new(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name": "nginx",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name": "nginx",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx",
						},
					},
				},
			},
		},
	})
	createAndCleanup(t, kubeClient, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 80,
				},
			},
		},
	})

	// Create Ingress ProxyGroup for each Tailnet.
	firstTailnetPG := &tsapi.ProxyGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "first-tailnet",
		},
		Spec: tsapi.ProxyGroupSpec{
			Type: tsapi.ProxyGroupTypeIngress,
		},
	}
	createAndCleanup(t, kubeClient, firstTailnetPG)
	secondTailnetPG := &tsapi.ProxyGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "second-tailnet",
		},
		Spec: tsapi.ProxyGroupSpec{
			Type:    tsapi.ProxyGroupTypeIngress,
			Tailnet: "second-tailnet",
		},
	}
	createAndCleanup(t, kubeClient, secondTailnetPG)

	// TODO: Verify that devices have been created in the expected Tailnet.

	// Apply Ingress to expose nginx.
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "second-tailnet",
			Namespace: "default",
			Annotations: map[string]string{
				"tailscale.com/proxy-group": "second-tailnet",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: new("tailscale"),
			TLS: []networkingv1.IngressTLS{
				networkingv1.IngressTLS{
					Hosts: []string{"second-tailnet"},
				},
			},
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: new(networkingv1.PathTypePrefix),
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "nginx",
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	createAndCleanup(t, kubeClient, ingress)

	if err := tstest.WaitFor(5*time.Minute, func() error {
		ing := &networkingv1.Ingress{}
		if err := kubeClient.Get(t.Context(), client.ObjectKey{
			Namespace: "default", Name: "second-tailnet",
		}, ing); err != nil {
			return err
		}
		if len(ing.Status.LoadBalancer.Ingress) == 0 ||
			ing.Status.LoadBalancer.Ingress[0].Hostname == "" {
			return fmt.Errorf("Ingress not ready yet")
		}
		t.Logf("Ingress hostname: %s", ing.Status.LoadBalancer.Ingress[0].Hostname)
		return nil
	}); err != nil {
		t.Fatalf("Ingress never got a hostname: %v", err)
	}

	// TODO: cleanup second tailnet
}
