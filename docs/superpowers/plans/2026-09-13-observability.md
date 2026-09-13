# Implementation plan: observability (OTel + Prometheus/Grafana/Tempo)

Spec: `docs/superpowers/specs/2026-09-13-observability-design.md`
Branch: `observability` (worktree `.worktrees/observability`, stacked on `ingress-ratelimit`).

Global constraints:
- Envoy Gateway v1.2.5; EnvoyProxy CRD fields verified against the chart CRDs.
- Plain manifests only (no new Helm charts); all images pinned by tag.
- Services must run unchanged when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset.
- Commit per task; verify `git rev-parse --abbrev-ref HEAD` = `observability`
  and toplevel = the worktree before every commit.

## Task 1 — monitoring stack manifests

`deploy/k8s/observability/`:
- `namespace.yaml` — `monitoring`.
- `otel-collector.yaml` — ConfigMap (otlp gRPC/http receivers → batch →
  otlp exporter to `tempo.monitoring:4317`, insecure), Deployment, Service
  (4317, 4318).
- `tempo.yaml` — ConfigMap (single-binary, otlp receiver, local storage under
  `/tmp/tempo`), Deployment, Service (4317 ingest, 3200 query).
- `prometheus.yaml` — ServiceAccount + ClusterRole/Binding (pods
  get/list/watch), ConfigMap with one kubernetes_sd `pod` job scoped to
  `envoy-gateway-system`, keeping pods labeled as EG proxies, target port
  19001, metrics path `/stats/prometheus`; Deployment, Service (9090).
- `grafana.yaml` — Deployment (anonymous auth, Admin role), Service (3000),
  provisioning ConfigMaps: datasources (Prometheus default + Tempo), one
  dashboard (gateway request rate by status incl. 429s, p50/p99 latency).

Verify: `kubectl apply --dry-run=client -f deploy/k8s/observability/`.

## Task 2 — gateway telemetry

- `deploy/k8s/envoyproxy.yaml`: EnvoyProxy `proxy-config` in
  `envoy-gateway-system` (metrics.prometheus, tracing samplingRate 100,
  OpenTelemetry backendRefs → `otel-collector.monitoring:4317`) plus a
  ReferenceGrant in `monitoring` (from EnvoyProxy/envoy-gateway-system to
  Service).
- `deploy/k8s/gatewayclass.yaml`: add `spec.parametersRef`.

Verify: dry-run apply; field paths match the extracted CRD schema.

## Task 3 — Go OTel instrumentation

- `go get` `go.opentelemetry.io/otel{,/sdk,/exporters/otlp/otlptrace/otlptracegrpc}`,
  `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`,
  `.../google.golang.org/grpc/otelgrpc`.
- `internal/otelsetup/otelsetup.go`: `Init(ctx, serviceName)` per spec (no-op
  without endpoint) + test covering the no-op path.
- Wire all five `cmd/*/main.go`; wrap the servers/clients listed in the spec.
- Add `OTEL_EXPORTER_OTLP_ENDPOINT` env to the five service manifests.

Verify: `go build ./... && go test ./... && gofmt -l . && go vet ./...`.

## Task 4 — Makefile + demo step 8

- Targets: `deploy-observability`; `up` order: cluster-up →
  deploy-observability → bootstrap-gateway-controller → deploy;
  bootstrap applies envoyproxy.yaml before gatewayclass.yaml; `grafana`
  port-forward target; extend `status` with monitoring pods.
- `scripts/demo.sh` step 8: port-forward prometheus (9090) and tempo (3200),
  poll ≤30 s for (a) non-empty Prometheus instant-query result for an
  `envoy_cluster_upstream_rq` metric from the gateway job and (b) Tempo
  search hit for `service.name=service-a`. Reuse the script's existing
  port-forward/cleanup conventions.

Verify: `bash -n scripts/demo.sh`; `make -n up`.

## Task 5 — README

Rationale ("why OTel/what you can see"), step-8 walkthrough, `make grafana`,
repo-layout entries, update step-count references (eight → nine).

## Task 6 — runtime acceptance

`make up && make demo` on a fresh k3d cluster from the worktree; all steps
0–8 green. Tear down with `make down` afterwards. Fix and re-run on failure.
