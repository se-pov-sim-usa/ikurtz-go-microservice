# Local developer convenience. The Harness pipeline does NOT need this file: it calls `go test`,
# scripts/image-tag.sh, the Docker build, and `helm` directly (see HARNESS.md).
SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c
.ONESHELL:
.DEFAULT_GOAL := help

# ---- config (override on the command line, e.g. make deploy ENV=prod CLUSTER=example-prod-use1-a) ----
AWS_REGION   ?= us-west-2
CLUSTER_NAME ?= rockery-demo
ECR_REPO     ?= rockery
ENV          ?= dev
CLUSTER      ?=
NAMESPACE    ?= rockery
RELEASE      ?= rockery
CHART        := charts/rockery

# Image tag strategy: git_sha (default) | semver | git_tag | existing.
#   semver reads ./VERSION unless TAG_VALUE is given; `existing` deploys an already-pushed tag (TAG_VALUE=<tag>).
TAG_STRATEGY ?= git_sha
TAG_VALUE    ?=

# Audit stamps written onto the pods. CI/pipelines override these (RUN_ID, TRIGGERED_BY).
RUN_ID       ?= local-$(shell date +%Y%m%d%H%M%S)
TRIGGERED_BY ?= $(shell whoami)

ACCOUNT_ID    = $(shell aws sts get-caller-identity --query Account --output text)
REGISTRY      = $(ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com
IMAGE_REPO    = $(REGISTRY)/$(ECR_REPO)
LOCAL_IMAGE  := rockery:local

export AWS_REGION CLUSTER_NAME ECR_REPO

.PHONY: help
help: ## Show this help
	awk 'BEGIN{FS=":.*##"; printf "\nUsage: make \033[36m<target>\033[0m\n"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

##@ Develop
.PHONY: run test lint
run: ## Run the service locally on :8080
	go run -ldflags "-X main.version=local" .

test: ## Unit tests with the race detector
	go test -race -count=1 -cover ./...

lint: ## gofmt check + go vet
	test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }
	go vet ./...

##@ Helm chart
.PHONY: helm-lint helm-template
helm-lint: ## Lint the chart and render every environment (catches schema/template errors)
	for env in dev preprod prod; do
	  echo "== $$env"
	  helm lint $(CHART) -f $(CHART)/values/$$env.yaml \
	    --set image.repository=example.invalid/rockery --set image.tag=0.0.0-lint
	done
	for f in $(CHART)/values/clusters/*.yaml; do
	  echo "== prod + $$f"
	  helm lint $(CHART) -f $(CHART)/values/prod.yaml -f "$$f" \
	    --set image.repository=example.invalid/rockery --set image.tag=0.0.0-lint
	done

helm-template: ## Render manifests for ENV (and optional CLUSTER) to stdout. Needs TAG_VALUE or a tag strategy.
	TAG="$$(./scripts/image-tag.sh $(TAG_STRATEGY) $(TAG_VALUE))"
	helm template $(RELEASE) $(CHART) --namespace $(NAMESPACE) \
	  -f $(CHART)/values/$(ENV).yaml $(if $(CLUSTER),-f $(CHART)/values/clusters/$(CLUSTER).yaml) \
	  --set image.repository=$${IMAGE_REPO:-example.invalid/rockery} --set image.tag="$$TAG"

##@ Container + ECR
.PHONY: tag docker-build docker-run ecr-create ecr-login push
tag: ## Print the image tag the chosen strategy would produce (validates inputs)
	./scripts/image-tag.sh $(TAG_STRATEGY) $(TAG_VALUE)

docker-build: ## Build the image locally (linux/amd64, matches EKS nodes)
	TAG="$$(./scripts/image-tag.sh $(TAG_STRATEGY) $(TAG_VALUE))"
	docker buildx build --platform linux/amd64 --load \
	  --build-arg VERSION="$$TAG" --build-arg COMMIT="$$(git rev-parse HEAD 2>/dev/null || echo none)" -t $(LOCAL_IMAGE) .

docker-run: docker-build ## Run the container locally on :8080
	docker run --rm -p 8080:8080 $(LOCAL_IMAGE)

ecr-create: ## Create the ECR repository (immutable tags, scan on push)
	./infra/ecr.sh

ecr-login: ## Docker login to ECR
	aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(REGISTRY)

push: ecr-login ## Build and push the image to ECR using the tag strategy (TAG_STRATEGY=git_sha|semver|git_tag)
	TAG="$$(./scripts/image-tag.sh $(TAG_STRATEGY) $(TAG_VALUE))"
	docker buildx build --platform linux/amd64 --push \
	  --build-arg VERSION="$$TAG" --build-arg COMMIT="$$(git rev-parse HEAD 2>/dev/null || echo none)" \
	  -t $(IMAGE_REPO):"$$TAG" .
	echo "Pushed $(IMAGE_REPO):$$TAG"
	echo "Deploy it with:  make deploy ENV=$(ENV) TAG_STRATEGY=$(TAG_STRATEGY) $(if $(TAG_VALUE),TAG_VALUE=$(TAG_VALUE))"

##@ Cloud: one-time setup
.PHONY: cluster-create cluster-addons kubeconfig cluster-delete
cluster-create: ## Create the EKS cluster (~15-20 min, costs money)
	eksctl create cluster -f infra/eksctl-cluster.yaml

cluster-addons: ## Install the AWS Load Balancer Controller
	./infra/alb-controller.sh

kubeconfig: ## Point kubectl at the cluster
	aws eks update-kubeconfig --name $(CLUSTER_NAME) --region $(AWS_REGION)

cluster-delete: ## Tear down the cluster (uninstalls the release first so the ALB is cleaned up)
	-helm uninstall $(RELEASE) --namespace $(NAMESPACE)
	eksctl delete cluster -f infra/eksctl-cluster.yaml --disable-nodegroup-eviction

##@ Deploy with Helm
.PHONY: deploy helm-test status history rollback url smoke logs undeploy release
deploy: ## helm upgrade --install for ENV (+CLUSTER). Atomic: a failed rollout rolls back automatically
	TAG="$$(./scripts/image-tag.sh $(TAG_STRATEGY) $(TAG_VALUE))"
	# Namespace carries the Pod Security label; created idempotently (Helm can't manage its own namespace).
	kubectl create namespace $(NAMESPACE) --dry-run=client -o yaml \
	  | kubectl label --local -f - pod-security.kubernetes.io/enforce=restricted -o yaml \
	  | kubectl apply -f -
	# Remember the revision we are replacing (empty on first install) so a failed post-deploy test can return to it.
	PREV_REV="$$(helm history $(RELEASE) --namespace $(NAMESPACE) --max 1 -o yaml 2>/dev/null | awk '/revision:/{print $$NF; exit}' || true)"
	helm upgrade --install $(RELEASE) $(CHART) --namespace $(NAMESPACE) \
	  -f $(CHART)/values/$(ENV).yaml $(if $(CLUSTER),-f $(CHART)/values/clusters/$(CLUSTER).yaml) \
	  --set image.repository=$(IMAGE_REPO) --set image.tag="$$TAG" --set tests.enabled=true \
	  --set-string deploy.runId="$(RUN_ID)" --set-string deploy.triggeredBy="$(TRIGGERED_BY)" \
	  --set-string deploy.gitSha="$$(git rev-parse HEAD 2>/dev/null || echo none)" \
	  --atomic --wait --timeout 5m
	# --atomic covers pods that never become ready. This covers pods that are ready but wrong
	# (the test checks the served version and environment): go back to the exact prior revision.
	if ! helm test $(RELEASE) --namespace $(NAMESPACE) --timeout 2m; then
	  echo "helm test FAILED" >&2
	  if [[ -n "$$PREV_REV" ]]; then
	    echo "Rolling back to revision $$PREV_REV" >&2
	    helm rollback $(RELEASE) "$$PREV_REV" --namespace $(NAMESPACE) --wait --timeout 5m
	  fi
	  exit 1
	fi

release: push deploy ## Push the image, then deploy it

helm-test: ## Run the chart's test pod (health + version + environment check)
	helm test $(RELEASE) --namespace $(NAMESPACE) --timeout 2m

status: ## What is deployed right now: release, chart, image tag, environment, cluster
	helm list --namespace $(NAMESPACE)
	kubectl -n $(NAMESPACE) get pods -L app.kubernetes.io/version,rockery.example.com/environment,rockery.example.com/cluster,rockery.example.com/cluster-group

history: ## Release history (revision, status, image/chart); each revision is a rollback target
	helm history $(RELEASE) --namespace $(NAMESPACE)

rollback: ## Roll back to the previous revision (or REV=<n>). No new build or pipeline run needed
	helm rollback $(RELEASE) $(REV) --namespace $(NAMESPACE) --wait --timeout 5m

url: ## Print the ALB URL (needs ingress enabled: preprod/prod, or --set ingress.enabled=true)
	kubectl -n $(NAMESPACE) get ingress -l app.kubernetes.io/instance=$(RELEASE) -o jsonpath='http://{.items[0].status.loadBalancer.ingress[0].hostname}{"\n"}'

smoke: ## Run the smoke test against the deployed ALB
	./scripts/smoke-test.sh "$$($(MAKE) --no-print-directory url)"

logs: ## Tail logs from all pods
	kubectl -n $(NAMESPACE) logs -l app.kubernetes.io/instance=$(RELEASE) --tail=50 -f --max-log-requests=12

undeploy: ## Uninstall the release (and its ALB)
	helm uninstall $(RELEASE) --namespace $(NAMESPACE)
