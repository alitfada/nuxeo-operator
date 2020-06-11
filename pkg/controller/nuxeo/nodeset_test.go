package nuxeo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"nuxeo-operator/pkg/apis/nuxeo/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TestBasicDeploymentCreation tests the basic mechanics of creating a new Deployment from the Nuxeo CR spec
// when a Deployment does not already exist
func (suite *nodeSetSuite) TestBasicDeploymentCreation() {
	nux := suite.nodeSetSuiteNewNuxeo()
	result, err := reconcileNodeSet(&suite.r, nux.Spec.NodeSets[0], nux, nux.Spec.RevProxy, log)
	require.Nil(suite.T(), err, "reconcileNodeSet failed with err: %v\n", err)
	require.Equal(suite.T(), reconcile.Result{Requeue: true}, result,
		"reconcileNodeSet returned unexpected result: %v\n", result)
	found := &appsv1.Deployment{}
	err = suite.r.client.Get(context.TODO(), types.NamespacedName{Name: deploymentName(nux, nux.Spec.NodeSets[0]),
		Namespace: suite.namespace}, found)
	require.Nil(suite.T(), err, "Deployment creation failed with err: %v\n", err)
	require.Equal(suite.T(), suite.nuxeoContainerName, found.Spec.Template.Spec.Containers[0].Name,
		"Deployment has incorrect container name: %v\n", found.Spec.Template.Spec.Containers[0].Name)
}

// TestDeploymentUpdated creates a Deployment, updates the Nuxeo CR, and verifies the Deployment was updated
func (suite *nodeSetSuite) TestDeploymentUpdated() {
	nux := suite.nodeSetSuiteNewNuxeo()
	_, _ = reconcileNodeSet(&suite.r, nux.Spec.NodeSets[0], nux, nux.Spec.RevProxy, log)
	newReplicas := nux.Spec.NodeSets[0].Replicas + 2
	nux.Spec.NodeSets[0].Replicas = newReplicas
	_, _ = reconcileNodeSet(&suite.r, nux.Spec.NodeSets[0], nux, nux.Spec.RevProxy, log)
	found := &appsv1.Deployment{}
	_ = suite.r.client.Get(context.TODO(), types.NamespacedName{Name: deploymentName(nux, nux.Spec.NodeSets[0]),
		Namespace: suite.namespace}, found)
	require.Equal(suite.T(), newReplicas, *found.Spec.Replicas,
		"Deployment has incorrect replica count: %v\n", *found.Spec.Replicas)
}

// TestInteractiveChanged tests when a nodeset is changed from interactive true to false and vice versa
func (suite *nodeSetSuite) TestInteractiveChanged() {
	// todo-me
}

// TestRevProxyDeploymentCreation is the same as TestBasicDeploymentCreation except it includes an Nginx rev proxy
func (suite *nodeSetSuite) TestRevProxyDeploymentCreation() {
	nux := suite.nodeSetSuiteNewNuxeo()
	nux.Spec.RevProxy = v1alpha1.RevProxySpec{
		Nginx: v1alpha1.NginxRevProxySpec{
			Image:           "foo",
			ImagePullPolicy: corev1.PullAlways,
		},
	}
	_, _ = reconcileNodeSet(&suite.r, nux.Spec.NodeSets[0], nux, nux.Spec.RevProxy, log)
	found := &appsv1.Deployment{}
	_ = suite.r.client.Get(context.TODO(), types.NamespacedName{Name: deploymentName(nux, nux.Spec.NodeSets[0]),
		Namespace: suite.namespace}, found)
	require.Equal(suite.T(), suite.imagePullPolicy, found.Spec.Template.Spec.Containers[1].ImagePullPolicy,
		"Deployment sidecar has incorrect pull policy: %v\n", found.Spec.Template.Spec.Containers[1].ImagePullPolicy)
}

// nodeSetSuite is the NodeSet test suite structure
type nodeSetSuite struct {
	suite.Suite
	r                  ReconcileNuxeo
	nuxeoName          string
	deploymentName     string
	namespace          string
	nuxeoContainerName string
	imagePullPolicy    corev1.PullPolicy
}

// SetupSuite initializes the Fake client, a ReconcileNuxeo struct, and various test suite constants
func (suite *nodeSetSuite) SetupSuite() {
	suite.r = initUnitTestReconcile()
	suite.nuxeoName = "testnux"
	suite.namespace = "testns"
	suite.deploymentName = "testclust"
	suite.nuxeoContainerName = "nuxeo"
	suite.imagePullPolicy = corev1.PullAlways
}

// AfterTest removes objects of the type being tested in this suite after each test
func (suite *nodeSetSuite) AfterTest(_, _ string) {
	obj := appsv1.Deployment{}
	_ = suite.r.client.DeleteAllOf(context.TODO(), &obj)
}

// This function runs the NodeSet unit test suite. It is called by 'go test' and will call every
// function in this file with a nodeSetSuite receiver that begins with "Test..."
func TestNodeSetUnitTestSuite(t *testing.T) {
	suite.Run(t, new(nodeSetSuite))
}

// nodeSetSuiteNewNuxeo creates a test Nuxeo struct suitable for the test cases in this suite.
func (suite *nodeSetSuite) nodeSetSuiteNewNuxeo() *v1alpha1.Nuxeo {
	return &v1alpha1.Nuxeo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suite.nuxeoName,
			Namespace: suite.namespace,
		},
		Spec: v1alpha1.NuxeoSpec{
			NodeSets: []v1alpha1.NodeSet{{
				Name:     suite.deploymentName,
				Replicas: 3,
			}},
		},
	}
}