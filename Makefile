IMG ?= ghcr.io/apo-z/zitadel-operator:latest
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.23.0

.PHONY: all
all: generate manifests helm build

.PHONY: generate
generate: ## Generate DeepCopy methods.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

.PHONY: manifests
manifests: ## Generate CRDs and RBAC.
	$(CONTROLLER_GEN) rbac:roleName=zitadel-operator crd paths="./..." output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test: generate fmt vet
	go test ./... -coverprofile cover.out

.PHONY: build
build: generate fmt vet
	go build -o bin/manager ./cmd

.PHONY: run
run: generate fmt vet
	go run ./cmd

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push:
	docker push $(IMG)

.PHONY: install
install: manifests ## Install CRDs into the current cluster.
	kubectl apply --server-side -k config/crd

.PHONY: uninstall
uninstall:
	kubectl delete -k config/crd

.PHONY: deploy
deploy: manifests ## Deploy the operator into the current cluster.
	cd config/manager && kustomize edit set image controller=$(IMG) 2>/dev/null || true
	kubectl apply --server-side -k config/default

.PHONY: undeploy
undeploy:
	kubectl delete -k config/default

E2E_DIR := test/e2e
ENVTEST_K8S_VERSION ?= 1.37.0

.PHONY: test-e2e
test-e2e: manifests ## Run the e2e tests against a real Zitadel (docker compose) and envtest.
	mkdir -p $(E2E_DIR)/machinekey && chmod 777 $(E2E_DIR)/machinekey
	docker compose -f $(E2E_DIR)/compose.yaml up -d --wait
	until [ -s $(E2E_DIR)/machinekey/pat.txt ]; do sleep 2; done
	until curl -sf http://localhost:18080/debug/ready >/dev/null; do sleep 2; done
	ZITADEL_URL=http://localhost:18080 \
	ZITADEL_SA_KEY_FILE=$(CURDIR)/$(E2E_DIR)/machinekey/sa.json \
	ZITADEL_PAT_FILE=$(CURDIR)/$(E2E_DIR)/machinekey/pat.txt \
	KUBEBUILDER_ASSETS="$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.23 use $(ENVTEST_K8S_VERSION) -p path)" \
	go test -tags e2e ./test/e2e/ -count=1 -v

.PHONY: e2e-down
e2e-down:
	docker compose -f $(E2E_DIR)/compose.yaml down -v
	rm -rf $(E2E_DIR)/machinekey

CHART_DIR := charts/zitadel-operator

.PHONY: helm
helm: manifests ## Copy the generated CRDs and RBAC rules into the Helm chart.
	mkdir -p $(CHART_DIR)/files $(CHART_DIR)/templates/crds
	sed -n '/^rules:/,$$p' config/rbac/role.yaml | tail -n +2 > $(CHART_DIR)/files/rules.yaml
	for f in config/crd/bases/*.yaml; do \
	  out=$(CHART_DIR)/templates/crds/$$(basename $$f | sed 's/^zitadel.zitadel-operator.io_//'); \
	  { echo '{{- if .Values.crds.install }}'; \
	    sed -e '/^---$$/d' -e 's/{{/@@O@@/g' -e 's/}}/@@C@@/g' -e 's/@@O@@/{{"{{"}}/g' -e 's/@@C@@/{{"}}"}}/g' -e '0,/^  annotations:$$/s//  annotations:\n    {{- if .Values.crds.keep }}\n    helm.sh\/resource-policy: keep\n    {{- end }}/' $$f; \
	    echo '{{- end }}'; } > $$out; \
	done

.PHONY: helm-lint
helm-lint: helm
	helm lint $(CHART_DIR) --strict
	helm template test $(CHART_DIR) --namespace zitadel-operator > /dev/null
