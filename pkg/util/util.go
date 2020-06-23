package util

import (
	"crypto/md5"
	goerrors "errors"

	"github.com/ghodss/yaml"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// see IsOpenShift() / SetIsOpenShift()
var isOpenShift = false

// Returns true if the operator is running in an OpenShift cluster. Else false = Kubernetes. False
// by default, unless SetIsOpenShift() was called prior to this call
func IsOpenShift() bool {
	return isOpenShift
}

// Sets operator state indicating that the operator believes it is running in an OpenShift cluster.
func SetIsOpenShift() {
	isOpenShift = true
}

var NuxeoServiceAccountName = "nuxeo"

// ObjectsDiffer generates a YAML from each passed object then generates an MD5 sum of each YAML and returns
// true if the MD5 sums differ, and false if the MD5 sums are the same. If the YAML generation fails, then the
// resulting error is returned, otherwise a nil error is returned. This function is intended for comparing
// CR specs. The underlying assumption is that any difference in a spec is actionable for the operator. So this
// handles two cases: 1) the Nuxeo CR is modified, and a dependent CR should look different as a result. 2) A
// cluster CR owned by the Nuxeo CR is modified independently of the Operator and is therefore out of sync
// with how the Operator would expect it to look. Note that this works in most cases but not all. For example,
// a PVC can be generated with nil values in the Spec Volume field and the cluster will fill the volume field
// in. So this function is only useful for Specs that the cluster doesn't alter.
func ObjectsDiffer(expected interface{}, actual interface{}) (bool, error) {
	var expMd5, actMd5 [md5.Size]byte
	var err error
	var bytes []byte

	if bytes, err = yaml.Marshal(expected); err != nil{
		return false, err
	}
	debugExp := string(bytes)
	expMd5 = md5.Sum(bytes)
	if bytes, err = yaml.Marshal(actual); err != nil{
		return false, err
	}
	debugAct := string(bytes)
	_ = debugAct
	_ = debugExp
	actMd5 = md5.Sum(bytes)
	return expMd5 != actMd5, nil
}

// GetNuxeoContainer walks the container array in the passed deployment and returns a ref to the container
// named "nuxeo". If not found, returns a nil container ref and an error.
func GetNuxeoContainer(dep *appsv1.Deployment) (*corev1.Container, error){
	for i := 0; i < len(dep.Spec.Template.Spec.Containers); i++ {
		if dep.Spec.Template.Spec.Containers[i].Name == "nuxeo" {
			return &dep.Spec.Template.Spec.Containers[i], nil
		}
	}
	return nil, goerrors.New("could not find a container named 'nuxeo' in the deployment")
}