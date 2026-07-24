.ONESHELL:
SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c

.PHONY: help build docker-build docker-run docker-stop k3d-up k3d-down

REVISION ?= $(shell git describe --tags --always)
IMAGE    ?= coolkit:$(REVISION)

K3D_CLUSTER  ?= alpha
ARGOCD_CHART ?= 10.2.1

help: ## Show this help list
	@ grep -E '^[a-z.A-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

build: src/out/coolkit ## Build the go binary into src/out/
src/out/coolkit:
	cd src && CGO_ENABLED=0 GOOS=linux go build -trimpath -o out/coolkit .

docker-build: ## Build docker image
	docker build --tag $(IMAGE) src/

docker-run: docker-build ## Run container locally
	docker run --name=coolkit --detach \
	--publish=8080:8080 --publish=2345:2345 $(IMAGE)
	docker logs --follow coolkit

docker-stop: ## Stop and remove local container
	docker kill coolkit
	docker rm coolkit

k3d-up: ## Create local k3d cluster, install coolkit as ArgoCD app-of-apps with all its dependencies
	if ! k3d cluster list "$(K3D_CLUSTER)" >/dev/null 2>&1; then
		k3d cluster create "$(K3D_CLUSTER)" --agents 3 --port "8081:80@loadbalancer"
	fi

	kubectl wait --for=condition=Ready nodes --all --timeout=60s

	helm upgrade --install argocd argo/argo-cd \
		--namespace argocd --create-namespace \
		--version "$(ARGOCD_CHART)" \
		-f argocd/values/argocd-k3d.yaml --wait

	kubectl apply -f argocd/root.yaml

	@ echo "ArgoCD UI: http://argocd.localhost:8081 (login: admin / coolkit)"


k3d-down: ## Delete the local k3d cluster
	k3d cluster delete "$(K3D_CLUSTER)"
