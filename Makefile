mkfile_path := $(abspath $(lastword $(MAKEFILE_LIST)))
current_dir := $(abspath $(patsubst %/,%,$(dir $(mkfile_path))))

.PHONY: all
all: help

##@ General

.PHONY: build/csi-rclone
build/csi-rclone:  ## Build the csi-rclone binary
	go build -o build/csi-rclone cmd/csi-rclone-plugin/main.go

build-csi-rclone: build/csi-rclone  ## Build the csi-rclone binary

.PHONY: tests
tests:  ## Run tests
	KUBECONFIG="$(current_dir)/build/kube/config" go test -v ./...

.PHONY: cleanup
cleanup:  ## Cleanup the build directory
	rm -rf build/*

# From the operator sdk Makefile
# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php
.PHONY: help
help:  ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Docker image

.PHONY: docker-image
docker-image-test:  ## Build the csi-rclone docker image for tests
	docker buildx build --tag csi-rclone:latest --load .

##@ Helm

.PHONY: helm-lint
helm-lint:  ## Lint the helm chart
	helm lint deploy/csi-rclone

.PHONY: build/kind/csi-rclone-chart.yaml
build/kind/csi-rclone-chart.yaml:  ## Render the csi-rclone helm charts for tests
	mkdir -p build/kind
	helm template -n csi-rclone csi-rclone deploy/csi-rclone > build/kind/csi-rclone-chart.yaml

helm-template-test:  build/kind/csi-rclone-chart.yaml ## Render the csi-rclone helm charts for tests

##@ Kind

.PHONY: kind-create-cluster
kind-create-cluster:  ## Create the kind cluster
	kind create cluster --name "${CLUSTER_NAME}"

.PHONY: kind-delete-cluster
kind-delete-cluster:  ## Delete the kind cluster
	kind delete cluster --name "${CLUSTER_NAME}"

.PHONY: build/kube/config
build/kube/config:  ## Get the kubeconfig for the kind cluster
	mkdir -p build/kube
	kind get kubeconfig --name "${CLUSTER_NAME}" > build/kube/config

kind-get-kubeconfig: build/kube/config  ## Get the kubeconfig for the kind cluster

.PHONY: kind-create-namespace
kind-create-namespace:  ## Create the namespace used for tests
	KUBECONFIG="$(current_dir)/build/kube/config" kubectl create namespace csi-rclone || true

.PHONY: kind-load-docker-image-test
kind-load-docker-image-test:  docker-image-test ## Load the csi-rclone docker image for tests
	kind load docker-image csi-rclone:latest --name "${CLUSTER_NAME}"

.PHONY: kind-depoy-test
kind-depoy-test:  ## Deploy the rendered test chart
	KUBECONFIG="$(current_dir)/build/kube/config" kubectl apply -f build/kind/csi-rclone-chart.yaml
