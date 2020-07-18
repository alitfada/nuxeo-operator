package nuxeo

import (
	"context"
	goerrors "errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"nuxeo-operator/pkg/apis/nuxeo/v1alpha1"
	"nuxeo-operator/pkg/util"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// all mount protections are under this directory in the Nuxeo container
	backingMountBase = "/etc/nuxeo-operator/binding/"
)

// Configures all backing services, resulting in volumes, mounts, secondary secrets, environment variables,
// and nuxeo.conf entries as needed to configure Nuxeo to connect to a backing service. Caller must handle
// the nuxeo.conf returned from the function by storing it the Operator-owned nuxeo.conf ConfigMap. The returned
// nuxeo.conf is a concatenation of all backing service nuxeo.conf entries (so it could be an empty string.)
func configureBackingServices(r *ReconcileNuxeo, instance *v1alpha1.Nuxeo, dep *appsv1.Deployment,
	reqLogger logr.Logger) (string, error) {
	nuxeoConf := ""
	for idx, backingService := range instance.Spec.BackingServices {
		var err error
		if !backingSvcIsValid(backingService) {
			return "", goerrors.New("invalid backing service definition at ordinal position "+strconv.Itoa(idx))
		}
		if backingService.Preconfigured.Type != "" {
			if backingService, err = xlatBacking(backingService.Preconfigured); err != nil {
				return "", err
			}
		}
		if err = configureBackingService(r, instance, backingService, dep, reqLogger); err != nil {
			return "", err
		}
		// accumulate each backing service's nuxeo.conf settings
		nuxeoConf = joinCompact("\n", nuxeoConf, backingService.NuxeoConf)
	}
	return nuxeoConf, nil
}

// Configures one backing service. Iterates all resources and bindings, calls helpers to add environment variables
// and mounts into the nuxeo container, and volumes in the passed deployment. May create a secondary secret if
// needed.
func configureBackingService(r *ReconcileNuxeo, instance *v1alpha1.Nuxeo, backingService v1alpha1.BackingService,
	dep *appsv1.Deployment, reqLogger logr.Logger) error {
	// 0-1 secondary secret  per backing service
	secondarySecret := defaultSecondarySecret(r, instance, backingService)
	for _, resource := range backingService.Resources {
		gvk := strings.ToLower(resource.Group + "." + resource.Version + "." + resource.Kind)
		// validating the projections here ensures that the switch statement below works
		if !projectionsAreValid(gvk, resource.Projections) {
			return goerrors.New("backing service resource " + resource.Name + " has invalid projections")
		}
		for i := 0; i < len(resource.Projections); i++ {
			var err error
			projection := resource.Projections[i]
			switch {
			case (isSecret(resource) || isConfigMap(resource)) && projection.Env != "":
				err = projectEnvFrom(resource, i, dep)
			case projection.Mount != "":
				err = projectMount(r, instance.Namespace, backingService.Name, resource, i, dep, &secondarySecret)
			case projection.Transform != (v1alpha1.CertTransform{}):
				err = projectTransform(r, instance.Namespace, backingService.Name, resource, i, dep, &secondarySecret, reqLogger)
			default:
				err = goerrors.New(fmt.Sprintf("no handler for projection at ordinal position %v in resource %s", i, resource.Name))
			}
			if err != nil {
				return err
			}
		}
	}
	return reconcileSecondary(r, instance, &secondarySecret, reqLogger)
}

// Adds an environment variable with a valueFrom that references the key in the passed resource, which must be a
// Secret or ConfigMap. Returns non-nil error if: passed resource is not a Secret or ConfigMap, or environment
// variable name is not unique in the nuxeo container. Otherwise nil error is returned and an environment variable
// is added to the nuxeo container in the passed deployment like:
//   env:
//   - name: ELASTIC_PASSWORD              # from projection.Env
//     valueFrom:
//       secretKeyRef:
//         key: elastic                    # from projection.Key
//         name: elastic-es-elastic-user   # from resource.Name
func projectEnvFrom(resource v1alpha1.BackingServiceResource, idx int, dep *appsv1.Deployment) error {
	projection := resource.Projections[idx]
	env := corev1.EnvVar{
		Name: projection.Env,
	}
	if isSecret(resource) {
		env.ValueFrom = &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: resource.Name},
				Key:                  projection.Key,
			},
		}
	} else if isConfigMap(resource) {
		env.ValueFrom = &corev1.EnvVarSource{
			ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: resource.Name},
				Key:                  projection.Key,
			},
		}
	} else {
		return goerrors.New("illegal operation: projectEnvFrom called with resource other than ConfigMap or Secret")
	}
	if nuxeoContainer, err := util.GetNuxeoContainer(dep); err != nil {
		return err
	} else if util.GetEnv(nuxeoContainer, env.Name) != nil {
		return goerrors.New("invalid backing service projection - attempt to add duplicate environment var: " + env.Name)
	} else {
		nuxeoContainer.Env = append(nuxeoContainer.Env, env)
	}
	return nil
}

// Handles mount projections for resources by creating/appending to a volume with a projection source like so:
//   volumes:
//   - name: backing-elastic
//     projected:
//       sources:
//       - secret:
//           name: tls-secret
//           items:
//           - key: ca.crt
//             path: ca.crt
// There will be one such volume and corresponding vol mount for each backing service specifying any mount
// projection in the Nuxeo CR like so:
//   backingServices:
//   - name: elastic # Nuxeo Operator creates volume "backing-elastic"
//     resources:
//     - version: v1
//       kind: secret
//       name: some-secret
//       projections:
//       - key: ca.crt
//         mount: ca.crt # becomes path in projection
// This function supports projecting certificates and similar values onto the filesystem so nuxeo.conf can reference
// them with explicit filesystem paths.
func projectMount(r *ReconcileNuxeo, namespace string, backingServiceName string,
	resource v1alpha1.BackingServiceResource, idx int, dep *appsv1.Deployment, secondarySecret *corev1.Secret) error {
	var nuxeoContainer *corev1.Container
	var err error
	if nuxeoContainer, err = util.GetNuxeoContainer(dep); err != nil {
		return err
	}
	vol := corev1.Volume{
		Name: strings.ToLower("backing-" + backingServiceName),
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				DefaultMode: util.Int32Ptr(420),
				Sources:     []corev1.VolumeProjection{},
			},
		},
	}
	var src corev1.VolumeProjection
	if isSecret(resource) {
		src = corev1.VolumeProjection{
			Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{Name: resource.Name},
				Items: []corev1.KeyToPath{{
					Key:  resource.Projections[idx].Key,
					Path: resource.Projections[idx].Mount,
				}},
			},
		}
	} else if isConfigMap(resource) {
		src = corev1.VolumeProjection{
			ConfigMap: &corev1.ConfigMapProjection{
				LocalObjectReference: corev1.LocalObjectReference{Name: resource.Name},
				Items: []corev1.KeyToPath{{
					Key:  resource.Projections[idx].Key,
					Path: resource.Projections[idx].Mount,
				}},
			},
		}
	} else {
		// configurer wants value from non-Secret/non-CM which isn't a supported Kubernetes projection type. So
		// copy the value into the secondary secret and use the secondary secret as the source of the mount. Caller
		// must reconcile secondary secret
		var val []byte
		var newKey string
		if val, _, err = getValueFromResource(r, resource, namespace, idx); err != nil || val == nil {
			return err
		}
		if newKey, err = pathToKey(resource.Projections[idx].Path); err != nil {
			return err
		}
		if _, ok := secondarySecret.Data[newKey]; ok {
			return goerrors.New("secondary secret " + secondarySecret.Name + " already contains key " + newKey)
		}
		// caller must reconcile
		secondarySecret.Data[newKey] = []byte(val)
		src = corev1.VolumeProjection{
			Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{Name: secondarySecret.Name},
				Items: []corev1.KeyToPath{{
					Key:  newKey,
					Path: resource.Projections[idx].Mount,
				}},
			},
		}
	}
	vol.VolumeSource.Projected.Sources = append(vol.VolumeSource.Projected.Sources, src)
	if err = addVolumeProjectionAndItems(dep, vol); err != nil {
		return err
	}
	volMnt := corev1.VolumeMount{
		Name:      vol.Name,
		ReadOnly:  true,
		MountPath: backingMountBase + backingServiceName,
	}
	return addVolMnt(nuxeoContainer, volMnt)
}

// first try - probably delete this
func projectMountSAVE(r *ReconcileNuxeo, namespace string, backingServiceName string,
	resource v1alpha1.BackingServiceResource, idx int, dep *appsv1.Deployment, secondarySecret *corev1.Secret) error {
	var nuxeoContainer *corev1.Container
	var err error
	if nuxeoContainer, err = util.GetNuxeoContainer(dep); err != nil {
		return err
	}
	_ = nuxeoContainer
	var vol corev1.Volume
	if isSecret(resource) {
		// mount Secret
		vol = corev1.Volume{
			Name: strings.ToLower(resource.Kind + "-" + resource.Name),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					DefaultMode: util.Int32Ptr(420),
					SecretName:  resource.Name,
					Items: []corev1.KeyToPath{{
						Key:  resource.Projections[idx].Key,
						Path: resource.Projections[idx].Mount,
					}},
				},
			},
		}
	} else if isConfigMap(resource) {
		// mount ConfigMap
		vol = corev1.Volume{
			Name: strings.ToLower(resource.Kind + "-" + resource.Name),
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					DefaultMode:          util.Int32Ptr(420),
					LocalObjectReference: corev1.LocalObjectReference{Name: resource.Name},
					Items: []corev1.KeyToPath{{
						Key:  resource.Projections[idx].Key,
						Path: resource.Projections[idx].Mount,
					}},
				},
			},
		}
	} else {
		// configurer wants value from non-Secret non-CM so copy the value into the secondary secret
		// and use the secondary secret as the source
		var val []byte
		var newKey string
		val, _, err = getValueFromResource(r, resource, namespace, idx)
		if err != nil {
			return err
		}
		newKey, err = pathToKey(resource.Projections[idx].Path)
		if err != nil {
			return err
		}
		if _, ok := secondarySecret.Data[newKey]; ok {
			return goerrors.New("secondary secret " + secondarySecret.Name + " already contains key " + newKey)
		}
		secondarySecret.Data[newKey] = []byte(val)

		vol = corev1.Volume{
			Name: strings.ToLower(resource.Kind + "-" + secondarySecret.Name),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					DefaultMode: util.Int32Ptr(420),
					SecretName:  secondarySecret.Name,
					Items: []corev1.KeyToPath{{
						Key:  newKey,
						Path: resource.Projections[idx].Mount,
					}},
				},
			},
		}
	}
	if err = addVolumeAndItems(dep, vol); err != nil {
		return err
	}
	volMnt := corev1.VolumeMount{
		Name:      vol.Name,
		ReadOnly:  true,
		MountPath: backingMountBase + backingServiceName,
	}
	return addVolMnt(nuxeoContainer, volMnt)
}

// todo-me this a truststore transform - will have to be refactored once the keystore transform is added for
//  two-way TLS (e.g. Strimzi)
// secret/cm  transform   create/update secondary secret, add transformed value as key, add
// other      transform   " along with transformation
func projectTransform(r *ReconcileNuxeo, namespace string, backingServiceName string, resource v1alpha1.BackingServiceResource,
	idx int, dep *appsv1.Deployment, secondarySecret *corev1.Secret, reqLogger logr.Logger) error {
	var resVer string
	var err error
	var resVal []byte
	var nuxeoContainer *corev1.Container

	storeKey := resource.Projections[idx].Transform.Store
	passKey := resource.Projections[idx].Transform.Password
	// some basic validation to protect against logic errors in the operator
	if _, ok := secondarySecret.Data[storeKey]; ok {
		return goerrors.New("key " + storeKey + " already defined in secret " + secondarySecret.Name)
	} else if _, ok := secondarySecret.Data[passKey]; ok {
		return goerrors.New("key " + passKey + " already defined in secret " + secondarySecret.Name)
	}
	if resVal, resVer, err = getValueFromResource(r, resource, namespace, idx); err != nil {
		return err
	}
	if secondarySecretIsCurrent(r, secondarySecret.Name, namespace, resource, resVer) {
		if err = loadSecondary(r, resource, namespace, secondarySecret, resVer, storeKey, passKey); err != nil {
			return err
		}
	} else if err = populateSecondaryNew(resource, secondarySecret, storeKey, passKey, resVer, resVal); err != nil {
		return err
	}
	// generate deployment/pod structs to support the projection
	vol := corev1.Volume{
		Name: strings.ToLower("backing-" + backingServiceName),
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				DefaultMode: util.Int32Ptr(420),
				Sources: []corev1.VolumeProjection{{
					Secret: &corev1.SecretProjection{
						LocalObjectReference: corev1.LocalObjectReference{Name: secondarySecret.Name},
						Items: []corev1.KeyToPath{{
							Key:  storeKey,
							Path: storeKey, // for now the path is the key maybe in future provide an override
						}, {
							Key:  passKey,
							Path: passKey,
						}},
					},
				}},
			},
		},
	}
	if err = addVolumeProjectionAndItems(dep, vol); err != nil {
		return err
	}
	volMnt := corev1.VolumeMount{
		Name:      vol.Name,
		ReadOnly:  true,
		MountPath: backingMountBase + backingServiceName,
	}
	if nuxeoContainer, err = util.GetNuxeoContainer(dep); err != nil {
		return err
	}
	if err = addVolMnt(nuxeoContainer, volMnt); err != nil {
		return err
	}
	// store password
	env := corev1.EnvVar{
		Name: resource.Projections[idx].Transform.PassEnv,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secondarySecret.Name},
				Key:                  passKey,
			},
		},
	}
	return util.OnlyAdd(nuxeoContainer, env)
}

// Gets the existing secondary secret from the cluster and populates the passed keys in the the passed in-mem
// secret so a subsequent reconcile of the secret has nothing to do. Also annotates the secret as would be
// done on initial creation.
func loadSecondary(r *ReconcileNuxeo, resource v1alpha1.BackingServiceResource, namespace string,
	secondarySecret *corev1.Secret, resVer string, keys ...string) error {
	obj := corev1.Secret{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: secondarySecret.Name, Namespace: namespace}, &obj); err != nil {
		// should not get here if the secondary secret does not exist
		return err
	}
	secondarySecret.Annotations[genAnnotationKey(resource)] = resVer
	for _, key := range keys {
		secondarySecret.Data[key] = obj.Data[key]
	}
	return nil
}

// Using the value in the resVal arg, which is expected to be 1 or more PEM-encoded certs, converts those certs
// into a password-protected Java trust store of type JKS. (P12 isn't presently supported.) If no error, then
// the secondary secret struct is updated with the truststore bits and the password
//
// args
//  resource - the upstream resource GVK+Name that contributed certificate bits - used to annotate the
//             secondary secret
//  secondarySecret - ref to the secondary secret that will hold the trust store and trust store pass
//                    generated by this fxn
//  storeKey - the key in secondarySecret to hold the trust store
//  passKey - the key in secondarySecret to hold the trust store password generated by this function
//  resVer - the resourceVersion of the upstream resource also used to annotate the secondary secret
//  resVal - the PEM-encoded certificate from the upstream resource to convert to the trust store
func populateSecondaryNew(resource v1alpha1.BackingServiceResource, secondarySecret *corev1.Secret, storeKey string,
	passKey string, resVer string, resVal []byte) error {

	secondarySecret.Annotations[genAnnotationKey(resource)] = resVer
	if store, pass, err := toTrustStoreFromBytes(resVal); err != nil {
		return err
	} else {
		secondarySecret.Data[storeKey] = store
		secondarySecret.Data[passKey] = []byte(pass)
		return nil
	}
}

// Converts a JSONPath expression to a valid Secret Key name by removing invalid characters
func pathToKey(jsonPath string) (string, error) {
	reg, err := regexp.Compile("[^-._a-zA-Z0-9]+")
	if err != nil {
		return "", err
	}
	return reg.ReplaceAllString(jsonPath, ""), nil
}

// Validates projections for the passed backing service resource based on resource GVK. These are the
// currently supported projections:
//
// Secrets and ConfigMaps
//
// Secret and ConfigMap resources 1) must specify .key, 2) must not specify .path, and 3) must specify one of: .mount,
// .env, or .transform. This means that only the projection key can be used to get a value from the secret/cm. It
// also means that the resulting value can be projected as an environment variable, a mount, or transformed into
// a secondary secret value.
//
// All other (e.g. Service)
//
// All other resources 1) must specify .path, 2) must not specify .key or .env, and 3) must specify one of .mount
// or .transform. This means that resources *other than* Secrets and ConfigMaps require a JSONPath expressions to get
// the resource value. And it means that the resulting value - which will ALWAYS be in a secondary secret - can only
// be projected as a mount, or transformed.
func projectionsAreValid(gvk string, projections []v1alpha1.ResourceProjection) bool {
	for _, projection := range projections {
		if gvk == ".v1.secret" || gvk == ".v1.configmap" {
			if projection.Key == "" || projection.Path != "" ||
				(projection.Env == "" && projection.Mount == "" && projection.Transform == (v1alpha1.CertTransform{})) {
				return false
			}
		} else if projection.Key != "" || projection.Path == "" || projection.Env != "" ||
			(projection.Mount == "" && projection.Transform == (v1alpha1.CertTransform{})) {
			return false
		}
	}
	return true
}

// returns true of the passed resourced is a Secret, else false
func isSecret(resource v1alpha1.BackingServiceResource) bool {
	return strings.ToLower(resource.Group+"."+resource.Version+"."+resource.Kind) == ".v1.secret"
}

// returns true of the passed resourced is a ConfigMap, else false
func isConfigMap(resource v1alpha1.BackingServiceResource) bool {
	return strings.ToLower(resource.Group+"."+resource.Version+"."+resource.Kind) == ".v1.configmap"
}

// Reconciles the passed secondary secret with the cluster. A secondary secret is one that is created for
// a backing service whenever a) a value is obtained from a backing service resource other than a Secret or
// ConfigMap, or b) a backing service value is transformed. In both cases, cluster storage is needed for the
// value and so the Operator creates a "secondary secret" to hold such values. There is 0-1 secondary
// secret per backing service.
func reconcileSecondary(r *ReconcileNuxeo, instance *v1alpha1.Nuxeo, secondarySecret *corev1.Secret,
	reqLogger logr.Logger) error {
	if len(secondarySecret.Data)+len(secondarySecret.StringData) != 0 {
		// secondary secret has content so it should exist in the cluster
		return addOrUpdate(r, secondarySecret.Name, instance.Namespace, secondarySecret, &corev1.Secret{},
			util.SecretCompare, reqLogger)
	} else {
		// secondary secret has no content so it not should exist in the cluster
		return removeIfPresent(r, instance, secondarySecret.Name, instance.Namespace, secondarySecret, reqLogger)
	}
}

// Gets the GVK+Name from the passed backing service resource struct, obtains the corresponding resource from
// the cluster using that GVK+Name, and gets a value from the cluster resource using the passed JSONPath expression
// if the object is not a Secret or ConfigMap, otherwise uses the projection key to get the value.
//
// Any issue results in non-nil return code. As with GetJsonPathValue, an empty return value and nil error can
// also indicate that the provided JSON path didn't find anything in the passed resource. If the requested
// resource does not exist in the cluster, a nil error is returned, and a nil value is returned.
//
// Returns [resource value] [resource version] [error]
func getValueFromResource(r *ReconcileNuxeo, resource v1alpha1.BackingServiceResource, namespace string,
	idx int) ([]byte, string, error) {
	gvk := strings.ToLower(resource.Group + "." + resource.Version + "." + resource.Kind)
	if gvk == ".v1.secret" {
		if resource.Projections[idx].Key == "" {
			return nil, "", goerrors.New("no key provided in projection")
		}
		obj := corev1.Secret{}
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: resource.Name, Namespace: namespace}, &obj); err != nil {
			if errors.IsNotFound(err) {
				return nil, "", nil
			}
			return nil, "", err
		}
		return obj.Data[resource.Projections[idx].Key], obj.ResourceVersion, nil
	} else if gvk == ".v1.configmap" {
		if resource.Projections[idx].Key == "" {
			return nil, "", goerrors.New("no key provided in projection")
		}
		obj := corev1.ConfigMap{}
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: resource.Name, Namespace: namespace}, &obj); err != nil {
			if errors.IsNotFound(err) {
				return nil, "", nil
			}
			return nil, "", err
		}
		return []byte(obj.Data[resource.Projections[idx].Key]), obj.ResourceVersion, nil

	} else {
		if resource.Projections[idx].Path == "" {
			return nil, "", goerrors.New("no path provided in projection")
		}
		schemaGvk := schema.GroupVersionKind{
			Group:   resource.Group,
			Version: resource.Version,
			Kind:    resource.Kind,
		}
		obj, err := r.scheme.New(schemaGvk)
		if err != nil {
			return nil, "", err
		}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: resource.Name, Namespace: namespace}, obj)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, "", nil
			}
			return nil, "", err
		}
		resVer := ""
		if rv, rve := util.GetJsonPathValue(obj, "{.metadata.resourceVersion}"); rve == nil && rv != nil {
			resVer = string(rv)
		}
		resVal, resErr := util.GetJsonPathValue(obj, resource.Projections[idx].Path)
		return resVal, resVer, resErr
	}
}

// creates and returns a secondary secret struct in the format required by the Operator
func defaultSecondarySecret(r *ReconcileNuxeo, instance *v1alpha1.Nuxeo, backingService v1alpha1.BackingService) corev1.Secret {
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        instance.Name + "-secondary-" + backingService.Name,
			Namespace:   instance.Namespace,
			Annotations: map[string]string{},
		},
		Data: map[string][]byte{},
		Type: corev1.SecretTypeOpaque,
	}
	_ = controllerutil.SetControllerReference(instance, &secret, r.scheme)
	return secret
}

// Generates an annotation key for a secondary secret like: nuxeo.operator.G.V.K.N (or nuxeo.operator.V.K.N if
// group is ""). E.g.: nuxeo.operator.v1.secret.elastic-es-http-certs-public. This annotation is used by the
// operator to know if an upstream resource that is transformed into a secondary secret has changed.
func genAnnotationKey(resource v1alpha1.BackingServiceResource) string {
	key := strings.Replace(fmt.Sprintf("nuxeo.operator.%s.%s.%s.%s",
		strings.ToLower(resource.Group),
		strings.ToLower(resource.Version),
		strings.ToLower(resource.Kind),
		strings.ToLower(resource.Name)), "..", ".", 1)
	return key
}

// if secondary secret exists in-cluster, and has secondary secret annotation, and annotation resource version
// is the same, then true, else false
func secondarySecretIsCurrent(r *ReconcileNuxeo, secondarySecret string, namespace string,
	resource v1alpha1.BackingServiceResource, resourceVersion string) bool {
	obj := corev1.Secret{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: secondarySecret, Namespace: namespace}, &obj); err != nil {
		return false
	} else {
		expectedAnnotation := genAnnotationKey(resource)
		if existingResVer, ok := obj.Annotations[expectedAnnotation]; ok {
			return existingResVer == resourceVersion
		}
	}
	return false
}

// A valid backing service specifies a preConfigured entry, in which case everything else is ignored, or, it
// specifies a name, and a resource list. A nuxeo.conf is optional
func backingSvcIsValid(backing v1alpha1.BackingService) bool {
	if !reflect.DeepEqual(backing.Preconfigured, v1alpha1.PreconfiguredBackingService{}) {
		return true
	} else {
		return backing.Name != "" && !reflect.DeepEqual(backing.Resources, v1alpha1.BackingServiceResource{})
	}
}

// Uses the passed preconfigured backing service to generate a backing service struct that will wire Nuxeo
// up to a backing service using well-known resources provided by the backing service.
func xlatBacking(preCfg v1alpha1.PreconfiguredBackingService) (v1alpha1.BackingService, error) {
	switch preCfg.Type {
	case v1alpha1.ECK:
		return eckBacking(preCfg), nil
	case v1alpha1.Strimzi:
		return v1alpha1.BackingService{}, goerrors.New("pre-config for Strimzi not implemented yet")
	case v1alpha1.Crunchy:
		return v1alpha1.BackingService{}, goerrors.New("pre-config for Crunchy not implemented yet")
	default:
		// can only happen if someone adds a preconfig and forgets to add a case statement for it
		return v1alpha1.BackingService{}, goerrors.New("unknown pre-configured backing service:"+string(preCfg.Type))
	}
}