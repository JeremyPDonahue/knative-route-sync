# knative-route-sync

A Kubernetes operator that watches Knative Services and automatically creates
OpenShift Routes so they are reachable through the cluster's ingress layer.

## Overview

On OpenShift clusters running Knative, the default Knative ingress (Kourier)
is not integrated with the OpenShift Router. This operator bridges the gap by
watching every Knative Service and reconciling three resources:

- A **ClusterIP Service** with manual Endpoints pointing at Kourier's internal
  ClusterIP — bridging the namespace boundary between the workload and
  `knative-serving`.
- An **Endpoints** object wiring the bridge Service to Kourier.
- An **OpenShift Route** targeting the bridge Service with the hostname derived
  from the Knative Service's status URL.

When a Knative Service is deleted, all three resources are cleaned up via a
finalizer.

## Prerequisites

- Go v1.24+
- kubectl v1.11.3+
- An OpenShift cluster with Knative Serving and Kourier installed
- Kourier internal Service available at `knative-serving/kourier-internal`

## Getting Started

### Run locally against a cluster

```sh
make run
```

### Build and deploy to a cluster

```sh
make docker-build docker-push IMG=<registry>/knative-route-sync:tag
make deploy IMG=<registry>/knative-route-sync:tag
```

### Install CRD manifests only

```sh
make install
```

### Uninstall

```sh
make undeploy
make uninstall
```

## Development

### Run tests

```sh
make test
```

Tests use `envtest` to spin up a real API server and etcd in-process. No
cluster required. The envtest binaries are downloaded automatically on first
run.

### View coverage

```sh
go tool cover -html=cover.out
```

## Architecture

The operator uses controller-runtime with a single controller —
`KnativeServiceReconciler` — that watches `serving.knative.dev/v1/Service`
objects and owns the bridge Service, Endpoints, and Route as child resources.

Mirror types for the Knative and OpenShift Route APIs live under `api/` as an
Anti-Corruption Layer, keeping the operator's dependency footprint minimal and
decoupled from upstream type changes.

## Known Technical Debt

- **`image: controller:latest` is a placeholder** — `config/manager/manager.yaml`
  must be updated with a real registry image before deploying to a cluster.
  Build and push with `make docker-build docker-push IMG=<registry>/knative-route-sync:tag`
  then deploy with `make deploy IMG=<registry>/knative-route-sync:tag`.

- **Test coverage at 80.7%** — partial deletion failure scenarios in
  `deleteRouteResources` (e.g. Route deleted but Endpoints deletion fails)
  are not covered. Requires error injection to test reliably.

## License

Copyright 2026. Licensed under the Apache License, Version 2.0.
