# Dedicated, disposable k3d cluster for this project. Deliberately NOT named
# after any pre-existing cluster you may already have — `make down` deletes
# this cluster outright, so it must never collide with unrelated work.
CLUSTER := envoy-experiment
KCTX := k3d-$(CLUSTER)
NAMESPACE := envoy-experiment
SERVICES := service-a service-b service-c service-d token-service
GOBIN := $(shell go env GOPATH)/bin
KUBECTL := kubectl --context $(KCTX)
HELM := helm --kube-context $(KCTX)
# Client-side mTLS flags for every curl that enters through the ingress.
# --resolve pins inbound.local to the local port-forward so TLS SNI and the
# server cert's SAN (inbound.local) line up.
TLSFLAGS := --cacert .certs/ca.crt --cert .certs/client.crt --key .certs/client.key --resolve inbound.local:8888:127.0.0.1

.PHONY: proto build docker-build k3d-import \
	cluster-up cluster-down bootstrap-gateway-controller \
	deploy-infra deploy-services deploy test clean up down status \
	token demo certs

## Regenerate Go code from proto/chain/v1/chain.proto via buf.
proto:
	PATH="$$PATH:$(GOBIN)" buf generate

## Compile all services locally (sanity check).
build:
	go build ./...

## Build one Docker image per service (linux/arm64 to match k3d's Docker runtime).
docker-build:
	@for s in $(SERVICES); do \
		echo "building $$s"; \
		docker build --build-arg SERVICE=$$s -t envoy-experiment/$$s:dev . ; \
	done

## Import built images into the k3d cluster's containerd.
k3d-import:
	k3d image import $(foreach s,$(SERVICES),envoy-experiment/$(s):dev) -c $(CLUSTER)

## Create the dedicated k3d cluster if it doesn't already exist. Traefik is
## disabled at creation time so it never installs its own Gateway API CRDs
## (which conflict with the ones bundled in the Envoy Gateway Helm chart).
## Host port 8081 is mapped to the cluster's built-in loadbalancer on port 80
## for convenience; testing still primarily uses kubectl port-forward.
cluster-up:
	@if k3d cluster list $(CLUSTER) >/dev/null 2>&1; then \
		echo "k3d cluster '$(CLUSTER)' already exists, skipping create"; \
	else \
		k3d cluster create $(CLUSTER) \
			--servers 1 --agents 0 \
			--port "8081:80@loadbalancer" \
			--k3s-arg "--disable=traefik@server:*" \
			--wait; \
	fi
	$(KUBECTL) wait --timeout=120s -n kube-system deployment/coredns --for=condition=Available

## Delete the dedicated k3d cluster entirely (also removes every resource
## this project created inside it — no need to `make clean` first).
cluster-down:
	-k3d cluster delete $(CLUSTER)

## Install/upgrade the Envoy Gateway controller + GatewayClass (idempotent).
bootstrap-gateway-controller:
	$(HELM) upgrade --install eg oci://docker.io/envoyproxy/gateway-helm --version v1.2.5 \
		-n envoy-gateway-system --create-namespace
	$(KUBECTL) wait --timeout=120s -n envoy-gateway-system deployment/envoy-gateway --for=condition=Available
	$(KUBECTL) apply -f deploy/k8s/gatewayclass.yaml

## Apply namespace + all service Deployments/Services.
deploy-services:
	$(KUBECTL) apply -f deploy/k8s/namespace.yaml
	$(KUBECTL) apply -f deploy/k8s/service-a.yaml -f deploy/k8s/service-b.yaml -f deploy/k8s/service-c.yaml -f deploy/k8s/service-d.yaml -f deploy/k8s/token-service.yaml

## Generate the demo mTLS PKI (CA + server + client certs) into .certs/.
certs:
	./scripts/gen-certs.sh

## Install both per-instance Envoy Gateway Helm releases. The inbound gateway
## terminates mTLS, so its server cert and client-validation CA are loaded as
## Secrets first (from the git-ignored .certs/ directory).
deploy-infra: certs
	$(KUBECTL) create secret tls inbound-gateway-tls -n $(NAMESPACE) \
		--cert=.certs/server.crt --key=.certs/server.key \
		--dry-run=client -o yaml | $(KUBECTL) apply -f -
	$(KUBECTL) create secret generic inbound-gateway-ca -n $(NAMESPACE) \
		--from-file=ca.crt=.certs/ca.crt \
		--dry-run=client -o yaml | $(KUBECTL) apply -f -
	$(HELM) upgrade --install inbound-gateway deploy/charts/inbound-gateway
	$(HELM) upgrade --install outbound-gateway deploy/charts/outbound-gateway

## Build, import, and apply everything onto an already-running cluster.
deploy: docker-build k3d-import deploy-services deploy-infra

## Full unattended bring-up from zero: create the cluster, install the
## gateway controller, build/import images, and deploy services + gateways.
up: cluster-up bootstrap-gateway-controller deploy
	@echo ""
	@echo "Ready. Run 'make demo' for the full walkthrough (or 'make test' for a quick smoke test)."

## Full teardown: delete the dedicated cluster (fastest, cleanest option).
down: cluster-down

## Quick health check across cluster, pods, gateways and routes.
status:
	@k3d cluster list $(CLUSTER) 2>&1 || true
	@echo "--- pods ---"; $(KUBECTL) get pods -n $(NAMESPACE) 2>&1 || true
	@echo "--- gateways ---"; $(KUBECTL) get gateway -n $(NAMESPACE) 2>&1 || true
	@echo "--- routes ---"; $(KUBECTL) get httproute,grpcroute -n $(NAMESPACE) 2>&1 || true

## Port-forward to the inbound gateway and exercise the full A -> B -> outbound gateway -> D chain.
test:
	@$(KUBECTL) port-forward -n envoy-gateway-system svc/inbound-gateway 8888:443 >/dev/null 2>&1 & \
	pf_pid=$$!; sleep 3; \
	tok=$$(curl -s $(TLSFLAGS) -X POST https://inbound.local:8888/auth/token | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p'); \
	curl -s $(TLSFLAGS) -H "Authorization: Bearer $$tok" -X POST https://inbound.local:8888/a/hello -d 'hello-from-make-test'; echo; \
	kill $$pf_pid

## Mint a JWT via the (open) /auth route and print an export-able line.
token:
	@$(KUBECTL) port-forward -n envoy-gateway-system svc/inbound-gateway 8888:443 >/dev/null 2>&1 & \
	pf_pid=$$!; sleep 3; \
	tok=$$(curl -s $(TLSFLAGS) -X POST https://inbound.local:8888/auth/token | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p'); \
	kill $$pf_pid; \
	echo "export TOKEN=$$tok"

## Run the five-step demo / E2E acceptance script.
demo:
	KCTX=$(KCTX) ./scripts/demo.sh

## Remove just this project's Kubernetes resources, keeping the cluster and
## gateway controller running (useful for iterating without a full rebuild).
clean:
	-$(HELM) uninstall inbound-gateway outbound-gateway
	-$(KUBECTL) delete -f deploy/k8s/service-a.yaml -f deploy/k8s/service-b.yaml -f deploy/k8s/service-c.yaml -f deploy/k8s/service-d.yaml -f deploy/k8s/token-service.yaml
	-$(KUBECTL) delete -f deploy/k8s/namespace.yaml
