package nuxeo

import (
	goerrors "errors"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"nuxeo-operator/pkg/apis/nuxeo/v1alpha1"
)

// addProbes adds liveness and readiness probes to the Nuxeo container spec in the passed deployment. If probes are
// defined in the passed NodeSet spec then they are used in their entirety. Otherwise they are defaulted:
//  httpGet:
//    path: /nuxeo/runningstatus
//    port: 8080
//  initialDelaySeconds: 5
//  timeoutSeconds: 3
//  periodSeconds: 10
//  successThreshold: 1
//	failureThreshold: 3
func addProbes(dep *appsv1.Deployment, nodeSet v1alpha1.NodeSet) error {
	// get the nuxeo container from the deployment. Don't assume any particular ordinal position in the
	// containers array but require the container name to be "nuxeo"
	var nuxeoContainer *corev1.Container
	for i := 0; i < len(dep.Spec.Template.Spec.Containers); i++ {
		if dep.Spec.Template.Spec.Containers[i].Name == "nuxeo" {
			nuxeoContainer = &dep.Spec.Template.Spec.Containers[i]
			break
		}
	}
	if nuxeoContainer == nil {
		// seems impossible but...
		return goerrors.New("could not find a nuxeo container in the deployment")
	}
	nuxeoContainer.LivenessProbe = defaultProbe()
	if nodeSet.LivenessProbe != nil {
		nodeSet.LivenessProbe.DeepCopyInto(nuxeoContainer.LivenessProbe)
	}
	nuxeoContainer.ReadinessProbe = defaultProbe()
	if nodeSet.ReadinessProbe != nil {
		nodeSet.ReadinessProbe.DeepCopyInto(nuxeoContainer.ReadinessProbe)
	}
	return nil
}

// defaultProbe creates and returns a pointer to a default liveness/readiness probe struct
func defaultProbe() *corev1.Probe {
	return &corev1.Probe{
		Handler: corev1.Handler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/nuxeo/runningstatus",
				Port: intstr.IntOrString{
					Type:   intstr.Int,
					IntVal: 8080,
				},
			},
		},
		InitialDelaySeconds: 5,
		TimeoutSeconds:      3,
		PeriodSeconds:       10,
		SuccessThreshold:    1,
		FailureThreshold:    3,
	}
}