ROOT                   := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
GOROOT                 := $(shell go env GOROOT)
OCICLI                 := docker
OPERATOR_VERSION       := 0.4.0
# if OpenShift, this is the OpenShift integrated container image registry
# If MicroK8s this is the internal registry plugin
IMAGE_REGISTRY         := default-route-openshift-image-registry.apps-crc.testing
IMAGE_REGISTRY_CLUST   := image-registry.openshift-image-registry.svc.cluster.local:5000
IMAGE_ORG              := images
# Registry in this context means custom operator registry for OLM
REGISTRY_NAMESPACE     := custom-operators
OPERATOR_IMAGE_NAME    := nuxeo-operator
BUNDLE_IMAGE_NAME      := nuxeo-operator-manifest-bundle
INDEX_IMAGE_NAME       := nuxeo-operator-index
OPERATOR_SDK_SUPPORTED := v0.18.0
OPERATOR_SDK_INSTALLED := $(shell operator-sdk version | cut -d, -f1 | cut -d: -f2 | sed "s/[[:blank:]]*\"//g")
# operator base image differs for OpenShift/Kubernetes. See build/Dockerfile
BASE_IMAGE_ARG         :=
UNIT_TEST_ARGS         ?= -v -coverprofile cp.out
E2E_TEST_ARGS          ?= --verbose
# used for e2e tests
NUXEO_IMAGE            ?= nuxeo:10.10

# Since Operator SDK is undergoing active development, check the version so that the Makefile is repeatable
ifneq ($(OPERATOR_SDK_SUPPORTED),$(OPERATOR_SDK_INSTALLED))
    $(error Requires operator-sdk: $(OPERATOR_SDK_SUPPORTED). Found: $(OPERATOR_SDK_INSTALLED))
endif

# set Make variables for MicroK8s
ifeq ($(TARGET_CLUSTER),MICROK8S)
    BASE_IMAGE_ARG       := --build-arg BASE_IMAGE=alpine
    IMAGE_REGISTRY       := localhost:32000
    IMAGE_REGISTRY_CLUST := localhost:32000
    E2E_KUBE_CONFIG_ARG  := --kubeconfig=/var/snap/microk8s/current/credentials/kubelet.config
endif

.PHONY : all
all:
	echo Run 'make help' to see a list of available targets

.PHONY : operator-unit-test
operator-unit-test:
	go test $(UNIT_TEST_ARGS) -run=UnitTestSuite $(ROOT)/pkg/controller/nuxeo/...

.PHONY : operator-e2e-test
operator-e2e-test:
	operator-sdk test local --debug $(ROOT)/test/e2e --operator-namespace operator-test $(E2E_TEST_ARGS)\
		--image $(IMAGE_REGISTRY_CLUST)/$(IMAGE_ORG)/$(OPERATOR_IMAGE_NAME):$(OPERATOR_VERSION)\
		$(E2E_KUBE_CONFIG_ARG)\
		--go-test-flags="--nuxeo-image=$(IMAGE_REGISTRY_CLUST)/$(IMAGE_ORG)/$(NUXEO_IMAGE)"

# CGO_ENABLED=0 seems standard and allows the executable go file to run on images that do not provide some libs
.PHONY : operator-build
operator-build:
	operator-sdk generate k8s
	operator-sdk generate crds
	CGO_ENABLED=0 go build -o $(ROOT)/build/_output/bin/nuxeo-operator $(ROOT)/cmd/manager

.PHONY : operator-image-build
operator-image-build:
	$(OCICLI) build --tag $(IMAGE_REGISTRY)/$(IMAGE_ORG)/$(OPERATOR_IMAGE_NAME):$(OPERATOR_VERSION)\
		--file $(ROOT)/build/Dockerfile $(BASE_IMAGE_ARG)\
		$(ROOT)/build

.PHONY : operator-image-push
operator-image-push:
	$(OCICLI) push $(IMAGE_REGISTRY)/$(IMAGE_ORG)/$(OPERATOR_IMAGE_NAME):$(OPERATOR_VERSION)

.PHONY : olm-generate
olm-generate:
	operator-sdk generate csv --csv-version $(OPERATOR_VERSION) --interactive=false --update-crds --csv-channel alpha

.PHONY : bundle-generate
bundle-generate:
	operator-sdk bundle create --generate-only --package nuxeo-operator --channels alpha --default-channel alpha

# https://sdk.operatorframework.io/docs/olm-integration/olm-deployment/#operator-sdk-run-packagemanifests-command-overview
# todo-me doesn't work: Error: unknown flag: --manifests-dir
.PHONY : bundle-test
bundle-test:
	operator-sdk run packagemanifests --olm-namespace openshift-operator-lifecycle-manager --operator-namespace nuxeo\
		--operator-version $(OPERATOR_VERSION) --manifests-dir deploy/olm-catalog/nuxeo-operator

# todo-me is it possible to not push the bundle image but rather to reference it locally in the index-add target? 
.PHONY : bundle-build
bundle-build:
	if ! grep OPERATOR_IMAGE\
		$(ROOT)/deploy/olm-catalog/nuxeo-operator/manifests/nuxeo-operator.clusterserviceversion.yaml >/dev/null 2>&1;\
		then echo "ERROR: Missing OPERATOR_IMAGE substitution token in nuxeo-operator.clusterserviceversion.yaml"; exit 1; fi
	# if building the bundle image, always include the latest CRD. Not crazy about this but it can't be done in
	# olm-generate because operator-sdk overwrites the CSV
	cp $(ROOT)/deploy/crds/nuxeo.com_nuxeos_crd.yaml $(ROOT)/deploy/olm-catalog/nuxeo-operator/manifests
	$(OCICLI) build --tag $(IMAGE_REGISTRY)/$(REGISTRY_NAMESPACE)/$(BUNDLE_IMAGE_NAME):$(OPERATOR_VERSION)\
		--file bundle.Dockerfile $(ROOT) $(BUNDLE_BUILD_ARG)\
		--build-arg OPERATOR_IMAGE=$(IMAGE_REGISTRY)/$(IMAGE_ORG)/$(OPERATOR_IMAGE_NAME):$(OPERATOR_VERSION)
	$(OCICLI) push $(IMAGE_REGISTRY)/$(REGISTRY_NAMESPACE)/$(BUNDLE_IMAGE_NAME):$(OPERATOR_VERSION)

.PHONY : index-add
index-add:
	opm index add --bundles $(IMAGE_REGISTRY)/$(REGISTRY_NAMESPACE)/$(BUNDLE_IMAGE_NAME):$(OPERATOR_VERSION)\
		--tag $(IMAGE_REGISTRY)/$(REGISTRY_NAMESPACE)/$(INDEX_IMAGE_NAME):$(OPERATOR_VERSION) --skip-tls\
		--container-tool $(OCICLI)

.PHONY : index-push
index-push:
	$(OCICLI) push $(IMAGE_REGISTRY)/$(REGISTRY_NAMESPACE)/$(INDEX_IMAGE_NAME):$(OPERATOR_VERSION)

.PHONY : apply-crd
apply-crd:
	kubectl delete crd/nuxeos.nuxeo.com
	cat $(ROOT)/deploy/crds/nuxeo.com_nuxeos_crd.yaml | kubectl create -f -

.PHONY : help
help:
	echo "$$HELPTEXT"

ifndef VERBOSE
.SILENT:
endif

.PHONY : print-%
print-%:
	$(info $($*))

export HELPTEXT
define HELPTEXT

This Make file provides targets to build the Nuxeo Operator version $(OPERATOR_VERSION) and install the Operator
into an OpenShift/Kubernetes cluster. It also builds and installs OLM components that enable the operator
to be deployed via an OLM subscription. This Make file assumes that all required dependencies are
already installed: go, operator-sdk, opm, and docker. The Make file runs silently unless you provide
a VERBOSE arg or variable. E.g.: make VERBOSE=

Targets:

operator-build        Builds the operator binary from Go sources. Note: 'go build' - as of 1.14 - seems to have
                      module-based behavior that is current-working-dir dependent. Therefore, since this project
                      uses Go modules, this target needs to run in the project root directory or, via the -C
                      make arg if running from outside project root.
operator-image-build  Builds a container image containing the Operator Go binary that was built by the
                      'operator-build' target, and includes other scripts generated by the Operator SDK that are
                      included in the source tree.
operator-image-push   Pushes the Operator container image to a registry identified by the IMAGE_REGISTRY and
                      IMAGE_ORG variables. This supports pushing to a public/private registry, as well as an
                      in-cluster registry. The current version of the Makefile defaults to
                      $(IMAGE_REGISTRY)/$(IMAGE_ORG)/$(OPERATOR_IMAGE_NAME):$(OPERATOR_VERSION) since this
                      version of the project is targeted at local testing. A future version will improve this.
operator-unit-test    Runs the Operator unit tests.
operator-e2e-test     Runs the Operator e2e tests. This target has a few pre-requisites that are documented
                      in the README.
olm-generate          Generates files under deploy/olm-catalog/nuxeo-operator/manifests to support creating an
                      installable Operator that integrates with OLM. Note - this *overwrites* the CSV each time so,
                      should only be run when the goal is to *replace* the CSV since the CSV currently contains
                      values that were hand-edited into the file after it was initially generated (e.g. RBACs.)
bundle-generate       Generates bundle.Dockerfile in project root, and annotations.yaml in
                      deploy/olm-catalog/nuxeo-operator/metadata. This target uses the output of 'olm-generate'
                      and is a precursor to packaging the operator for deployment as an OLM Index.
bundle-build          Creates a container image from outputs of 'bundle-generate' that are included in the source tree.
index-add             Creates an OLM Index image using the output of the 'bundle-build' target. Uses the 'opm'
                      command, which is built from https://github.com/operator-framework/operator-registry.
index-push            Pushes the Nuxeo Operator Index image to the cluster in the $(REGISTRY_NAMESPACE) namespace.
                      This is what enables OLM to create the Operator via a subscription.
help                  Prints this help.
print-%               A diagnostic tool. Prints the value of a Make variable. E.g. 'make print-OPERATOR_VERSION' to
                      print the value of 'OPERATOR_VERSION'.

To build and install the Nuxeo Operator into a test cluster from a clean cloned copy of this repository, execute
the following Make targets in order:

 1. operator-build       Build the operator binary
 2. operator-image-build Build the operator container image
 3. operator-image-push  Push the operator container image to the cluster
 4. bundle-build         Build the Operator bundle image
 5. index-add            Generate an OLM Index image from the bundle image
 6. index-push           Push the OLM Index image to the cluster

After completing the steps above, the Operator should be fully installed and accessible in the cluster. The
README provides documentation on subscribing the Operator and using it to bring up a Nuxeo cluster.

endef
