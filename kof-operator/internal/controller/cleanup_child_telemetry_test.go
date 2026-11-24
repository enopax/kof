/*
Copyright 2025.

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

package controller

import (
	"context"
	"testing"

	kcmv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	vmv1beta1 "github.com/VictoriaMetrics/operator/api/operator/v1beta1"
	"github.com/k0rdent/kof/kof-operator/internal/controller/record"
	"github.com/k0rdent/kof/kof-operator/internal/controller/vmuser"
	"github.com/k0rdent/kof/kof-operator/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	k8sevents "k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Ein geloeschter Kindcluster darf seinen Telemetrie-Zugang nicht behalten.
//
// Er behielt ihn: auf einem regionalen Cluster standen vier VMUser fuer
// Cluster, die es seit Stunden bis Monaten nicht mehr gab, und der Zugang des
// zuletzt geloeschten las noch 41 Zeitreihen aus den sieben Tagen davor. Wer
// die kubeconfig eines geloeschten Clusters noch irgendwo liegen hat - ein
// Export, ein Backup, ein CI-Secret -, behielt damit Lesezugriff auf die
// Telemetrie des ganzen Projekts.
//
// Geloescht wurde bis dahin nur die Verteilung der ConfigMap. Siehe
// enopax/enopax#45.
func TestCleanupChildRemovesTelemetryAccess(t *testing.T) {
	record.DefaultRecorder = new(k8sevents.FakeRecorder)

	const clusterName = "dev-dose"
	const namespace = "kcm-system"

	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		scheme.AddToScheme,
		corev1.AddToScheme,
		kcmv1beta1.AddToScheme,
		vmv1beta1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}

	// So heisst der Zugang: abgeleitet aus dem Namen der ConfigMap des Kindes,
	// damit er sich auch dann noch finden laesst, wenn das ClusterDeployment
	// schon weg ist und nichts mehr daraus gelesen werden kann.
	vmUserName := GetVMUserName(GetConfigMapName(clusterName), namespace)

	objects := []client.Object{
		&vmv1beta1.VMUser{ObjectMeta: metav1.ObjectMeta{
			Name: vmuser.BuildVMUserName(vmUserName), Namespace: namespace,
		}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: vmuser.BuildSecretName(vmUserName), Namespace: namespace,
		}},
		// Dasselbe Secret liegt in **zwei** Namespaces - das zweite wurde
		// genauso vergessen wie das erste.
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: vmuser.BuildSecretName(vmUserName), Namespace: k8s.KofNamespace,
		}},
		&kcmv1beta1.MultiClusterService{ObjectMeta: metav1.ObjectMeta{
			Name: vmuser.BuildMCSName(vmUserName),
		}},
		&kcmv1beta1.MultiClusterService{ObjectMeta: metav1.ObjectMeta{
			Name: GetChildConfigMapPropagationName(clusterName),
		}},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()

	if _, err := CleanupChild(context.Background(), c, clusterName, namespace); err != nil {
		t.Fatalf("CleanupChild: %v", err)
	}

	for _, o := range objects {
		key := types.NamespacedName{Name: o.GetName(), Namespace: o.GetNamespace()}
		err := c.Get(context.Background(), key, o.DeepCopyObject().(client.Object))
		if err == nil {
			t.Errorf("%T %s is still there after the cluster was deleted", o, key)
			continue
		}
		if !errors.IsNotFound(err) {
			t.Errorf("unexpected error for %s: %v", key, err)
		}
	}
}

// Ein Cluster, den es nie gab, darf das Aufraeumen nicht scheitern lassen -
// der Reconciler laeuft auch fuer Ereignisse, zu denen nichts existiert.
func TestCleanupChildIsIdempotent(t *testing.T) {
	record.DefaultRecorder = new(k8sevents.FakeRecorder)

	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		scheme.AddToScheme, corev1.AddToScheme, kcmv1beta1.AddToScheme, vmv1beta1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(s).Build()

	if _, err := CleanupChild(context.Background(), c, "never-existed", "kcm-system"); err != nil {
		t.Fatalf("CleanupChild on nothing: %v", err)
	}
}
