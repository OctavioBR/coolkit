# coolkit Helm chart

Deploys **coolkit** — an internal, high-throughput HTTP API with a distributed in-memory cache — onto a single
Kubernetes (GKE) cluster.

| Trait | How the chart handles it |
|---|---|
| Peer-to-peer distributed cache | `Deployment` + a **headless Service** for peer discovery; Downward API env vars |
| Dynamic autoscaling (10–100) | **KEDA `ScaledObject`** using the `metrics-api` scaler (no manual HPA) |
| Split traffic / observability planes | API on `8080`, health + Prometheus metrics on `2345` |
| Cloud SQL persistence | Direct private-IP connection with credentials from a Kubernetes Secret |
| Internal-only exposure | `ClusterIP` Service in the `production` namespace (no ingress/LB) |

> **Full operations runbook:** [../docs/OPERATIONS.md](../docs/OPERATIONS.md) ·
> **Multi-cluster scaling:** [../docs/SCALING.md](../docs/SCALING.md) ·
> **Future hardening (Cloud SQL Auth Proxy + Workload Identity, etc.):** [../docs/IMPROVEMENTS.md](../docs/IMPROVEMENTS.md)

## Prerequisites (cluster-level, installed once — not Helm deps)

- **KEDA** (`keda.sh`) — provides the `ScaledObject`/`TriggerAuthentication` CRDs and the operator.
- **Prometheus Operator** (e.g. kube-prometheus-stack) — for the `ServiceMonitor` (optional; annotation scraping is
  an alternative).
- A reachable **Cloud SQL** instance (private IP on the cluster VPC) and a **Secret** with `DB_USER` / `DB_PASSWORD`.
- The scaler endpoint `http://scaler.production.svc.cluster.local/estimate/coolkit` reachable in-cluster.

## Install / upgrade

Deployments are pinned to an **immutable image SHA** — the chart refuses to render without `image.tag`
(no `latest`).

```sh
helm upgrade --install coolkit ./coolkit \
  --namespace production --create-namespace \
  --set image.tag=<GIT_OR_IMAGE_SHA> \
  --set cloudsql.host=<CLOUD_SQL_PRIVATE_IP> \
  --set cloudsql.existingSecret=coolkit-db \
  --set ports.cache=<REAL_CACHE_PORT>
```

Per-region overrides are layered via values files (see [../argocd/](../argocd/)):

```sh
helm upgrade --install coolkit ./coolkit -n production \
  -f coolkit/values-example-region.yaml --set image.tag=<SHA>
```

## Validate locally (no cluster required)

```sh
helm lint coolkit/ --set image.tag=deadbeef
helm template coolkit coolkit/ -n production --set image.tag=deadbeef -a monitoring.coreos.com/v1
```

## Key values

| Key | Default | Notes |
|---|---|---|
| `image.repository` | `gcr.io/example/coolkit` | |
| `image.tag` | `""` | **Required**, immutable SHA. Render fails if empty. |
| `resources` | 2 CPU / 4Gi (requests == limits) | Guaranteed QoS. |
| `ports.http` / `ports.metrics` / `ports.cache` | `8080` / `2345` / `7000` | `cache` is a **placeholder** — set to the real port. |
| `peerDiscovery.mode` | `dns` | `dns` or `k8s-api` (adds a namespaced RBAC `Role`). |
| `keda.minReplicas` / `maxReplicas` | `10` / `100` | |
| `keda.scaler.url` | `…scaler.production…/estimate/coolkit` | |
| `keda.scaler.targetValue` | `"1"` | Endpoint returns a desired count ⇒ replicas track it 1:1. |
| `cloudsql.existingSecret` | `""` | **Recommended.** Out-of-band Secret with `DB_USER`/`DB_PASSWORD`. |
| `cloudsql.createSecret` | `false` | Dev-only; mutually exclusive with `existingSecret`. |
| `podDisruptionBudget.maxUnavailable` | `1` | Fixed, not a percentage (see OPERATIONS.md). |
| `metrics.serviceMonitor.enabled` | `true` | Skipped gracefully if the Operator CRD is absent. |
| `metrics.annotations.enabled` | `false` | Annotation-based scraping fallback. |
| `networkPolicy.enabled` | `false` | Restrict ingress on shared clusters. |

See [values.yaml](values.yaml) for the fully-commented surface.

## What the chart renders

`Deployment` · `Service` (API + metrics) · headless `Service` (peer discovery) · `ConfigMap` · `ServiceAccount` ·
`ScaledObject` (KEDA) · `PodDisruptionBudget` · `ServiceMonitor` (guarded) · `helm test` pod — plus, when enabled:
`Secret` (dev), `TriggerAuthentication`, RBAC `Role`/`RoleBinding` (k8s-api discovery), `NetworkPolicy`.

There is intentionally **no `hpa.yaml`** — KEDA owns the HPA. There is **no Cloud SQL Auth Proxy sidecar** in the
default path; it is documented as a hardening option in [../docs/IMPROVEMENTS.md](../docs/IMPROVEMENTS.md).
