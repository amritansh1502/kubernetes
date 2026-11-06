/*
Copyright The Kubernetes Authors.

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

package pods

import (
	"context"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	utilfeature "k8s.io/apiserver/pkg/util/feature"
	clientset "k8s.io/client-go/kubernetes"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	kubeapiservertesting "k8s.io/kubernetes/cmd/kube-apiserver/app/testing"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/test/integration/framework"
)

func httpProbeProtocolPod(name string, protocol *v1.HTTPProbeProtocol) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Name:  "container",
				Image: "fakeimage",
				LivenessProbe: &v1.Probe{
					ProbeHandler: v1.ProbeHandler{
						HTTPGet: &v1.HTTPGetAction{
							Path:     "/healthz",
							Port:     intstr.FromInt32(8080),
							Scheme:   v1.URISchemeHTTP,
							Protocol: protocol,
						},
					},
				},
			}},
		},
	}
}

func httpProbeProtocol(p *v1.Pod) *v1.HTTPProbeProtocol {
	return p.Spec.Containers[0].LivenessProbe.HTTPGet.Protocol
}

func TestHTTPProbeProtocolPod(t *testing.T) {
	server := kubeapiservertesting.StartTestServerOrDie(t, nil, framework.DefaultTestServerFlags(), framework.SharedEtcd())
	defer server.TearDownFn()

	client := clientset.NewForConfigOrDie(server.ClientConfig)
	ns := framework.CreateNamespaceOrDie(client, "pod-http-probe-protocol", t)
	defer framework.DeleteNamespaceOrDie(client, ns, t)

	t.Run("HTTP2 is persisted when the feature gate is enabled", func(t *testing.T) {
		featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.HTTPProbeProtocol, true)

		created, err := client.CoreV1().Pods(ns.Name).Create(context.TODO(), httpProbeProtocolPod("protocol-enabled", ptr.To(v1.HTTPProbeProtocolHTTP2)), metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("failed to create pod: %v", err)
		}
		if got := httpProbeProtocol(created); got == nil || *got != v1.HTTPProbeProtocolHTTP2 {
			t.Fatalf("expected protocol HTTP2 to be persisted, got %v", got)
		}
	})

	t.Run("protocol is dropped when the feature gate is disabled", func(t *testing.T) {
		featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.HTTPProbeProtocol, false)

		created, err := client.CoreV1().Pods(ns.Name).Create(context.TODO(), httpProbeProtocolPod("protocol-disabled", ptr.To(v1.HTTPProbeProtocolHTTP2)), metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("failed to create pod: %v", err)
		}
		if got := httpProbeProtocol(created); got != nil {
			t.Fatalf("expected protocol to be dropped, got %v", *got)
		}

		stored, err := client.CoreV1().Pods(ns.Name).Get(context.TODO(), created.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("failed to get pod: %v", err)
		}
		if got := httpProbeProtocol(stored); got != nil {
			t.Fatalf("expected protocol to be dropped from storage, got %v", *got)
		}
	})

	t.Run("HTTP2 combined with scheme HTTPS is rejected", func(t *testing.T) {
		featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.HTTPProbeProtocol, true)

		pod := httpProbeProtocolPod("protocol-https-rejected", ptr.To(v1.HTTPProbeProtocolHTTP2))
		pod.Spec.Containers[0].LivenessProbe.HTTPGet.Scheme = v1.URISchemeHTTPS
		_, err := client.CoreV1().Pods(ns.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
		assertHTTPProbeProtocolError(t, err, "HTTP2 cannot be combined with scheme HTTPS")
	})

	t.Run("HTTP2 combined with host set is rejected", func(t *testing.T) {
		featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.HTTPProbeProtocol, true)

		pod := httpProbeProtocolPod("protocol-host-rejected", ptr.To(v1.HTTPProbeProtocolHTTP2))
		pod.Spec.Containers[0].LivenessProbe.HTTPGet.Host = "10.0.0.1"
		_, err := client.CoreV1().Pods(ns.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
		assertHTTPProbeProtocolError(t, err, "host must be empty when protocol is HTTP2")
	})

	t.Run("protocol is preserved on update while the feature gate is enabled", func(t *testing.T) {
		featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.HTTPProbeProtocol, true)

		created, err := client.CoreV1().Pods(ns.Name).Create(context.TODO(), httpProbeProtocolPod("protocol-preserved-enabled", ptr.To(v1.HTTPProbeProtocolHTTP2)), metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("failed to create pod: %v", err)
		}

		created.Labels = map[string]string{"updated": "true"}
		updated, err := client.CoreV1().Pods(ns.Name).Update(context.TODO(), created, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update pod: %v", err)
		}
		if got := httpProbeProtocol(updated); got == nil || *got != v1.HTTPProbeProtocolHTTP2 {
			t.Fatalf("expected protocol HTTP2 to be preserved on update, got %v", got)
		}
	})

	t.Run("protocol is preserved on update when the feature gate is disabled", func(t *testing.T) {
		featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.HTTPProbeProtocol, true)

		created, err := client.CoreV1().Pods(ns.Name).Create(context.TODO(), httpProbeProtocolPod("protocol-preserved-disabled", ptr.To(v1.HTTPProbeProtocolHTTP2)), metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("failed to create pod: %v", err)
		}

		// Disabling the gate must not drop a protocol that is already in use
		// by the existing pod.
		featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.HTTPProbeProtocol, false)

		created.Labels = map[string]string{"updated": "true"}
		updated, err := client.CoreV1().Pods(ns.Name).Update(context.TODO(), created, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update pod: %v", err)
		}
		if got := httpProbeProtocol(updated); got == nil || *got != v1.HTTPProbeProtocolHTTP2 {
			t.Fatalf("expected protocol HTTP2 to remain set on existing pod, got %v", got)
		}
	})
}

func assertHTTPProbeProtocolError(t *testing.T, err error, wantMessage string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected object to be rejected, but it was accepted")
	}
	if !apierrors.IsInvalid(err) {
		t.Fatalf("expected an Invalid error, got %v", err)
	}
	if !strings.Contains(err.Error(), wantMessage) {
		t.Fatalf("expected error to contain %q, got %v", wantMessage, err)
	}
}
