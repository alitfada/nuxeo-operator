package nuxeo

import (
	"context"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	"nuxeo-operator/pkg/apis/nuxeo/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// updateNuxeoStatus updates the status field in the Nuxeo CR being watched by the operator. This is a
// very crude implementation and will be expanded in a later version
func updateNuxeoStatus(r *ReconcileNuxeo, nux *v1alpha1.Nuxeo, reqLogger logr.Logger) (reconcile.Result, error) {
	deployments := appsv1.DeploymentList{}
	opts := []client.ListOption{
		client.InNamespace(nux.Namespace),
	}
	availableNodes := int32(0)
	if err := r.client.List(context.TODO(), &deployments, opts...); err == nil {
		for _, dep := range deployments.Items {
			if nux.IsOwner(dep.ObjectMeta) {
				availableNodes += dep.Status.AvailableReplicas
			}
		}
		nux.Status.AvailableNodes = availableNodes
	} else {
		reqLogger.Error(err, "Failed to list deployments for Nuxeo", "Namespace", nux.Namespace, "Name", nux.Name)
		return reconcile.Result{}, err
	}
	if err := r.client.Status().Update(context.TODO(), nux); err != nil {
		reqLogger.Error(err, "Failed to update Nuxeo status", "Namespace", nux.Namespace, "Name", nux.Name)
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}
