package v1alpha1

import (
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/networking/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"nuxeo-operator/pkg/util"
)

// NodeSet defines the structure of the Nuxeo cluster. Each NodeSet results in a Deployment. This supports the
// capability to define different configurations for a Deployment of interactive Nuxeo nodes vs a Deployment
// of worker Nuxeo nodes.
type NodeSet struct {
	// The name of this node set. In cases where only one node set is needed, a recommended naming strategy is
	// to name this node set 'cluster'. For example, if you generate a Nuxeo CR named 'my-nuxeo' into the namespace
	// being watched by the Nuxeo Operator, and you name this node set 'cluster'. Then the operator will create
	// a deployment from the node set named 'my-nuxeo-cluster'
	Name string `json:"name"`

	// +kubebuilder:validation:Minimum=1
	// Populates the 'spec.replicas' property of the Deployment generated by this node set.
	Replicas int32 `json:"replicas"`

	// +kubebuilder:validation:Optional
	// Indicates whether this NodeSet will be accessible outside the cluster. Default is 'false'. If 'true', then
	// the Service created by the operator will be have its selectors defined such that it selects the Pods
	// created by this NodeSet. Exactly one NodeSet must be configured for external access.
	// +optional
	Interactive bool `json:"interactive,omitempty"`

	// +kubebuilder:validation:Optional
	// Supports adding environment variables into the Nuxeo container created by the Operator for this NodeSet. If
	// the PodTemplate is specified, these environment variables are ignored and the environment variables from the
	// PodTemplate - whether they are explicitly defined or not - are used.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// +kubebuilder:validation:Optional
	// Provides the ability to override hard-coded pod defaults, enabling fine-grained control over the
	// configuration of the Pods in the Deployment.
	// +optional
	PodTemplate corev1.PodTemplateSpec `json:"podTemplate,omitempty"`
}

// ServiceSpec provides the ability to minimally customize the the type of Service generated by the Operator
type ServiceSpec struct {
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// Specifies the Service type to create
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`

	// Specifies the port exposed by the service
	// +optional
	Port int32 `json:"port,omitempty"`

	// Specifies the port that the service will use internally to communicate with the Nuxeo cluster
	// +optional
	TargetPort int32 `json:"targetPort,omitempty"`
}

type NuxeoTLSTerminationType string

// NuxeoAccess supports creation of an OpenShift Route supporting access to the Nuxeo Service from outside of the
// cluster. In a future version, a Kubernetes Ingress object will be supported
type NuxeoAccess struct {
	// Specifies the host name. This is incorporated by the Operator into the operator-generated
	// OpenShift Route and should be accessible from outside the cluster via DNS or some other suitable
	// routing mechanism
	Hostname string `json:"hostname"`

	// Selects a target port in the Service backed by this NuxeoAccess spec. By default, 'web' is
	// populated by the Operator - which finds the default 'web' port in the Service generated by the Operator
	// +optional
	TargetPort intstr.IntOrString `json:"targetPort,omitempty"`

	// +kubebuilder:validation:Optional
	// Specifies the name of a secret with fields required to configure ingress for TLS, as determined by
	// the termination field. Example fields expected in such a secret are - 'key', 'certificate', and
	// 'caCertificate'. This is ignored, unless 'termination' is specified
	// +optional
	TLSSecret string `json:"tlsSecret,omitempty"`

	// +kubebuilder:validation:Optional
	// Specifies the TLS termination type. E.g. 'edge', 'passthrough', etc.
	// +optional
	// todo-me consider operator-defined (platform-agnostic) Type and associated Consts rather than OpenShift
	Termination routev1.TLSTerminationType `json:"termination,omitempty"`
}

// NginxRevProxySpec defines the configuration elements needed for the Nginx reverse proxy.
type NginxRevProxySpec struct {
	// Defines a ConfigMap that contains an 'nginx.conf' key, and a 'proxy.conf' key, each of which provide required
	// configuration to the Nginx container
	ConfigMap string `json:"configMap"`

	// References a secret containing keys 'tls.key', 'tls.cert', and 'dhparam' which are used to terminate
	// the Nginx TLS connection.
	Secret string `json:"secret"`

	// Specifies the image
	// +optional
	Image string `json:"image,omitempty"`

	// +kubebuilder:validation:Optional
	// Image pull policy. If not specified, then if 'image' is specified with the :latest tag,
	// then this is 'Always', otherwise it is 'IfNotPresent'. Note that this flows through to a Pod ultimately,
	// and pull policy is immutable in a Pod spec. Therefore if any changes are made to this value in a Nuxeo
	// CR once the Operator has generated a Deployment from the CR, subsequent Deployment reconciliations will fail.
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
}

// DummyRevProxySpec is intended to support testing in the future, and to stub out the ability to specify
// different reverse proxies in the 'RevProxySpec' struct
type DummyRevProxySpec struct{}

// RevProxySpec defines the reverse proxies supported by the Nuxeo Operator. Details are provided in the individual
// specs.
type RevProxySpec struct {
	// +kubebuilder:validation:Optional
	// nginx supports configuration of Nginx as the reverse proxy
	// +optional
	Nginx NginxRevProxySpec `json:"nginx,omitempty"`

	// +kubebuilder:validation:Optional
	// dummy supports testing
	// +optional
	Dummy DummyRevProxySpec `json:"dummy,omitempty"`
}

// Defines the desired state of a Nuxeo cluster
type NuxeoSpec struct {

	// +kubebuilder:validation:Optional
	// Overrides the default Nuxeo container image selected by the Operator. By default, the Operator
	// uses 'nuxeo:latest' as the container image. To override that, include the image spec here. Any allowable
	// form is supported. E.g. 'image-registry.openshift-image-registry.svc.cluster.local:5000/custom-images/nuxeo:custom'
	// +optional
	NuxeoImage string `json:"nuxeoImage,omitempty"`

	// +kubebuilder:validation:Optional
	// Image pull policy. If not specified, then if 'nuxeoImage' is specified with the :latest tag, then this is
	// 'Always', otherwise it is 'IfNotPresent'. Note that this flows through to a Pod ultimately, and pull policy
	// is immutable in a Pod spec. Therefore if any changes are made to this value in a Nuxeo CR once the
	// Operator has generated a Deployment from the CR, subsequent Deployment reconciliations will fail.
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// +kubebuilder:validation:Optional
	// Causes a reverse proxy to be included in the Nuxeo interactive deployment. The reverse proxy will
	// receive traffic from the Route/Ingress object created by the Operator, and forward that traffic to the Nuxeo
	// Service created by the operator, which in turn will forward traffic to the Nuxeo interactive Pods. Presently,
	// Nginx is the only supported option but the structure is intended to allow other implementations in the future.
	// If omitted, then no reverse proxy is created and traffic goes directly to the Nuxeo Pods.
	// +optional
	RevProxy RevProxySpec `json:"revProxy,omitempty"`

	// +kubebuilder:validation:Optional
	// Provides the ability to minimally customize the type of Service generated by the Operator.
	// +optional
	Service ServiceSpec `json:"serviceSpec,omitempty"`

	// +kubebuilder:validation:Optional
	// Defines how Nuxeo will be accessed externally to the cluster. It results in the creation of an
	// OpenShift Route object. In the future, it will also support generation of a Kubernetes Ingress object
	// +optional
	Access NuxeoAccess `json:"access,omitempty"`

	// +kubebuilder:validation:MinItems=1
	// Each nodeSet causes a Deployment to be created with the specified number of replicas, and other
	// characteristics specified within the nodeSet spec. At least one nodeSet is required
	NodeSets []NodeSet `json:"nodeSets"`
}

// NuxeoStatus defines the observed state of a Nuxeo cluster. This is preliminary and will be expanded in later
// versions
type NuxeoStatus struct {
	AvailableNodes int32 `json:"availableNodes,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Nuxeo is the Schema for the nuxeos API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=nuxeos,scope=Namespaced
type Nuxeo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NuxeoSpec   `json:"spec,omitempty"`
	Status NuxeoStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// NuxeoList contains a list of Nuxeo
type NuxeoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Nuxeo `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Nuxeo{}, &NuxeoList{})
	if registerOpenShiftRoute() {
		// by default: not OpenShift
		util.SetIsOpenShift()
	} else if !registerKubernetesIngress() {
		panic("Unable to register either an OpenShift Route or a Kubernetes Ingress to the SchemaBuilder")
	}
}

// registerOpenShiftRoute registers OpenShift Route types with the Scheme Builder. Returns true if
// success (e.g. running on OpenShift), else false
func registerOpenShiftRoute() bool {
	const GroupName = "route.openshift.io"
	const GroupVersion = "v1"
	SchemeGroupVersion := schema.GroupVersion{Group: GroupName, Version: GroupVersion}
	addKnownTypes := func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&routev1.Route{},
			&routev1.RouteList{},
		)
		metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
		return nil
	}
	SchemeBuilder := runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme := SchemeBuilder.AddToScheme
	return AddToScheme(scheme.Scheme) != nil
}

// registerKubernetesIngress registers Kubernetes Ingress types with the Scheme Builder. Returns true if
// success (e.g. running on Kubernetes), else false.
// Note: https://kubernetes.io/blog/2019/07/18/api-deprecations-in-1-16/ says:
// Migrate to use the networking.k8s.io/v1beta1 API version, available since v1.14
func registerKubernetesIngress() bool {
	const GroupName = "networking.k8s.io"
	const GroupVersion = "v1beta1"
	SchemeGroupVersion := schema.GroupVersion{Group: GroupName, Version: GroupVersion}
	addKnownTypes := func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&v1beta1.Ingress{},
			&v1beta1.IngressList{},
		)
		metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
		return nil
	}
	SchemeBuilder := runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme := SchemeBuilder.AddToScheme
	if err := AddToScheme(scheme.Scheme); err != nil {
		return false
	}
	return true
}