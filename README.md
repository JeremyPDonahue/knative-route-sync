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

## Deploying

Before deploying to a cluster, build and push the operator image to a registry
of your choice, then deploy using that image:

```sh
make docker-build docker-push IMG=<registry>/knative-route-sync:<tag>
make deploy IMG=<registry>/knative-route-sync:<tag>
```

The `image` field in `config/manager/manager.yaml` is intentionally left as a
placeholder (`REGISTRY/knative-route-sync:TAG`) since the target registry
differs per user.

## License

Copyright 2026. Licensed under the Apache License, Version 2.0.
