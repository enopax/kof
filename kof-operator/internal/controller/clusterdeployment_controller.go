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
	"fmt"
	"time"

	kcmv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/k0rdent/kof/kof-operator/internal/controller/vmuser"
	"github.com/k0rdent/kof/kof-operator/internal/k8s"
	"github.com/k0rdent/kof/kof-operator/internal/models/labels"
	"github.com/k0rdent/kof/kof-operator/internal/telemetry"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const MinRetryDelay = 1 * time.Second
const MaxRetryDelay = 15 * time.Second

// ClusterDeploymentReconciler reconciles a ClusterDeployment object
type ClusterDeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=clusterdeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=clusterdeployments/status,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ClusterDeployment object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *ClusterDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, endSpan := telemetry.StartReconcileSpan(ctx, "ClusterDeployment", req.Name, req.Namespace)
	defer endSpan()

	log := log.FromContext(ctx)

	clusterDeployment := k8s.GetClusterDeploymentStub(req.Name, req.Namespace)
	if err := r.Get(ctx, types.NamespacedName{
		Name:      req.Name,
		Namespace: req.Namespace,
	}, clusterDeployment); err != nil {
		if errors.IsNotFound(err) {
			return CleanupChild(ctx, r.Client, req.Name, req.Namespace)
		}

		log.Error(err, "cannot read clusterDeployment")
		return ctrl.Result{}, err
	}

	if !labels.HasClusterNameLabel(clusterDeployment.Labels) {
		return ctrl.Result{}, addClusterNameLabel(ctx, r.Client, clusterDeployment)
	}

	if err := r.ReconcileKofClusterRole(ctx, clusterDeployment); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kcmv1beta1.ClusterDeployment{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](
				MinRetryDelay,
				MaxRetryDelay,
			),
		}).
		Complete(r)
}

func addClusterNameLabel(ctx context.Context, client client.Client, cd *kcmv1beta1.ClusterDeployment) error {
	log := log.FromContext(ctx)
	if cd.Labels == nil {
		cd.Labels = make(map[string]string)
	}
	cd.Labels[labels.ClusterNameLabel] = cd.Name

	log.Info("Adding cluster name label to ClusterDeployment", "name", cd.Name, "namespace", cd.Namespace)
	if err := client.Update(ctx, cd); err != nil {
		return fmt.Errorf("failed to add cluster name label to ClusterDeployment %s/%s: %v", cd.Namespace, cd.Name, err)
	}
	return nil
}

// CleanupChild removes what a deleted child cluster leaves behind.
//
// Two things, and only the first one used to happen: the propagation of the
// child ConfigMap, and the **telemetry access** of the child - its VMUser, its
// credentials secret in both namespaces, and the MultiClusterService that
// propagates them to the regional cluster.
//
// Leaving the access behind is not harmless. It keeps authenticating after the
// cluster is gone, and it reads the telemetry of the whole project; four such
// users stood on a regional cluster for clusters deleted hours to months
// earlier, one of them still answering queries. Whoever kept a copy of a
// deleted cluster's kubeconfig kept the access. See enopax/enopax#45.
func CleanupChild(ctx context.Context, client client.Client, clusterName, namespace string) (ctrl.Result, error) {
	if result, err := cleanupChildConfigMapMcsPropagation(ctx, client, clusterName); err != nil {
		return result, err
	}

	// Derived from the ConfigMap name, the same way the access was created -
	// so this finds it even though the ClusterDeployment is already gone and
	// nothing can be read from it any more.
	vmUserName := GetVMUserName(GetConfigMapName(clusterName), namespace)
	if err := vmuser.NewManager(client).Delete(ctx, vmUserName, namespace); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to delete telemetry access of child cluster %s/%s: %v", namespace, clusterName, err)
	}

	return ctrl.Result{}, nil
}

// Function deletes the MultiClusterService created to propagate child ConfigMap to the region cluster.
// TODO: Remove this function once KCM implements automatic copying of the required resources to region clusters.
//
// Nicht exportiert, und das ist Absicht: wer ein Kind aufraeumt, raeumt
// **beides** auf - die Verteilung und den Zugang. Solange diese Funktion von
// aussen erreichbar war, liess sich die Haelfte aufrufen, und genau das stand
// im Reconciler. Siehe enopax/enopax#45.
func cleanupChildConfigMapMcsPropagation(ctx context.Context, client client.Client, clusterName string) (ctrl.Result, error) {
	mcs := &kcmv1beta1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name: GetChildConfigMapPropagationName(clusterName),
		},
	}

	if err := client.Delete(ctx, mcs); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to delete child config map propagation MultiClusterService: %v", err)
	}

	return ctrl.Result{}, nil
}
