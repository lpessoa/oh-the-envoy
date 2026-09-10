# envoy-experiment

A small Go project demonstrating two independent Envoy Gateway instances on
a k3d/Kubernetes cluster — one acting as an **inbound** (ingress) gateway,
the other as an **outbound** (egress) gateway — fronting a 3-hop gRPC/HTTP
chain of Go services.

## Architecture

```
client --HTTP/2--> [inbound-gateway]  --HTTP/2--> service-a
                                                       │ gRPC (direct, in-cluster)
                                                       ▼
                                                  service-b
                                                       │ gRPC
                                                       ▼
                                       [outbound-gateway] --gRPC--> service-d
```

- **service-a** — exposes an HTTP/2 (h2c) endpoint behind the *inbound*
  gateway. Converts each incoming HTTP request into a `ChainRequest` and
  calls **service-b** over gRPC, directly (ClusterIP, no gateway involved).
- **service-b** — gRPC server. Calls **service-d**, but dials the
  **outbound** gateway's stable Service instead of service-d directly,
  setting the gRPC `:authority` to `service-d.internal` so the outbound
  gateway's `GRPCRoute` matches and forwards to service-d. This simulates an
  egress-gateway routing pattern.
- **service-d** — terminal gRPC server; replies with a message. Each hop
  appends its name to a `hops` list so the final HTTP response shows the
  whole chain.

The proto contract lives in `proto/chain/v1/chain.proto` (single
`ChainService.Process` RPC) and is compiled with [buf] into `gen/`.

[buf]: https://buf.build

## Repo layout

```
proto/chain/v1/chain.proto     Proto contract (source of truth)
gen/chain/v1/                  buf-generated Go code
cmd/{service-a,service-b,service-d}/main.go   Entrypoints
internal/{servicea,serviceb,serviced}/        Business logic per service
internal/envutil/                             Tiny env-var helper
deploy/k8s/                     Namespace + Deployments/Services for the 3 Go services
deploy/charts/inbound-gateway/  Helm chart: Gateway + HTTPRoute for service-a
deploy/charts/outbound-gateway/ Helm chart: Gateway + GRPCRoute for service-d
Dockerfile                      Multi-stage build, select service via --build-arg SERVICE=
Makefile                        proto/build/docker-build/k3d-import/deploy/test/clean targets
```

Each gateway instance is its own Helm release. Both share the same
Envoy Gateway controller and `envoy` `GatewayClass`, installed once per
cluster by `make bootstrap-gateway-controller` (run automatically as part of
`make up`) — Envoy Gateway's controller is a cluster-wide singleton; each
`Gateway` resource it manages gets its own Envoy proxy Deployment, which is
what gives us two truly separate "instances".

Because the Envoy proxy Service that Envoy Gateway auto-creates per Gateway
has a non-deterministic, hash-suffixed name, each chart also creates a
stable-named Service (`inbound-gateway` / `outbound-gateway`, in
`envoy-gateway-system`) that selects the same proxy pods via their
`gateway.envoyproxy.io/owning-gateway-name` label. This is what service-b
dials, and what you use for local port-forwarding.

## Prerequisites

- `docker`, `k3d`, `helm`, `kubectl`, `go`, `buf` on your PATH.
- Nothing else — this project creates and destroys its own dedicated k3d
  cluster, named `envoy-experiment` (configurable via `CLUSTER=` on any
  `make` target). It never touches any other cluster you may have.

## Full lifecycle (unattended)

```
make up      # create dedicated k3d cluster, install Envoy Gateway controller,
             # build/import images, deploy services + both gateway instances
make test    # exercise the full chain through the inbound gateway
make down    # delete the dedicated k3d cluster (removes everything)
```

`make up` disables k3d's built-in Traefik at cluster-creation time so it
never installs its own (conflicting) Gateway API CRDs — Envoy Gateway's Helm
chart brings its own compatible set.

## Granular targets

Individual steps, useful once the cluster already exists:

- `make cluster-up` / `make cluster-down` — just the k3d cluster.
- `make bootstrap-gateway-controller` — install/upgrade Envoy Gateway + GatewayClass.
- `make proto`, `make build`, `make docker-build`, `make k3d-import`
- `make deploy-services`, `make deploy-infra`, `make deploy` (build+import+apply, cluster must already exist)
- `make clean` — remove this project's k8s resources, keep the cluster and gateway controller running.
- `make status` — quick health check (pods, gateways, routes).

## Test the full chain

```
make test
```
Or manually:
```
kubectl port-forward -n envoy-gateway-system svc/inbound-gateway 8888:80 &
curl --http2-prior-knowledge -H "Host: inbound.local" \
  -X POST http://localhost:8888/hello -d 'hello'
```

Expected response:
```json
{"message":"hello from service-d, payload=...","hops":["service-a","service-b","service-d","service-a(response)"],"received_protocol":"HTTP/2.0"}
```

## Regenerating proto code

Edit `proto/chain/v1/chain.proto`, then:
```
make proto
```
(requires `protoc-gen-go` and `protoc-gen-go-grpc` on `$GOPATH/bin`, invoked
by `buf` per `buf.gen.yaml`).

## Cleanup

```
make down
```
Deletes the dedicated `envoy-experiment` k3d cluster and everything in it —
the fastest, most complete teardown. Use `make clean` instead if you want to
keep the cluster and Envoy Gateway controller running between iterations.
