# coolkit — Operations Runbook

Operational guide for **coolkit**, an internal high-throughput HTTP API with a distributed in-memory cache,
KEDA-driven autoscaling, and Cloud SQL persistence. Deployed to the `production` namespace on GKE.

- Chart: [`../coolkit/`](../coolkit/) · Multi-cluster: [SCALING.md](SCALING.md) · Hardening: [IMPROVEMENTS.md](IMPROVEMENTS.md)

---

## 1. Architecture at a glance

```
        ┌─────────────────────── production namespace ───────────────────────┐
        │                                                                     │
 in-cluster        Service (ClusterIP)            KEDA ScaledObject           │
 consumers  ─────▶  coolkit :8080 (api)   ◀───────  metrics-api trigger  ─────┼──▶ scaler.production…/estimate/coolkit
                    coolkit :2345 (metrics)          (10..100 replicas)       │
                          │                                                   │
 Prometheus ──scrape──────┘                                                   │
                                                                              │
        │   Deployment (coolkit)  ──── pods gossip ────▶  headless Service    │
        │   ┌──────────┐  ┌──────────┐  ┌──────────┐      coolkit-headless    │
        │   │ pod :7000│◀▶│ pod :7000│◀▶│ pod :7000│  (peer discovery, DNS)   │
        │   └────┬─────┘  └────┬─────┘  └────┬─────┘                          │
        └────────┼─────────────┼─────────────┼───────────────────────────────┘
                 └─────────────┴─────────────┴───▶ Cloud SQL (private IP, creds from Secret)
```

- **Workload:** `Deployment` (not StatefulSet) — the cache is in-memory/ephemeral and must scale 10→100 fast in
  parallel, which StatefulSet's ordered rollout can't do.
- **Peer discovery:** headless Service returns all pod IPs; the app builds its cache-peer list from DNS (default) or
  the K8s API (`peerDiscovery.mode=k8s-api`).
- **Autoscaling:** KEDA owns the replica count via its own HPA (`keda-hpa-coolkit`). The chart sets **no** static
  replicas and **no** manual HPA.

---

## 2. Prerequisites

| Prerequisite | Why | Owner |
|---|---|---|
| KEDA installed (operator + CRDs) | `ScaledObject` autoscaling | Platform |
| Prometheus Operator (kube-prometheus-stack) | `ServiceMonitor` scraping (optional) | Platform |
| Cloud SQL instance w/ private IP on cluster VPC | Persistence | DBA / Platform (out of scope here) |
| Secret `coolkit-db` with `DB_USER`, `DB_PASSWORD` | App DB auth | Platform / secrets pipeline |
| Scaler service reachable at the configured URL | Feeds the estimate to KEDA | Owning team |
| Real cache/gossip port known | `ports.cache` default `7000` is a placeholder | App team |

Create the DB Secret out-of-band (recommended over letting the chart create it):

```sh
kubectl create secret generic coolkit-db -n production \
  --from-literal=DB_USER=coolkit \
  --from-literal=DB_PASSWORD='<password>'
```

---

## 3. Deploy

Ordinary deploy (immutable SHA, no `latest`):

```sh
helm upgrade --install coolkit ./coolkit -n production --create-namespace \
  --set image.tag=<SHA> \
  --set cloudsql.host=<PRIVATE_IP> --set cloudsql.existingSecret=coolkit-db \
  --set ports.cache=<REAL_CACHE_PORT>
```

Watch it land:

```sh
kubectl rollout status deploy/coolkit -n production
kubectl get scaledobject,hpa,pods -n production -l app.kubernetes.io/instance=coolkit
helm test coolkit -n production        # runs the health/API connectivity probe pod
```

**Rollback** to the previous good release:

```sh
helm history coolkit -n production
helm rollback coolkit <REVISION> -n production
```

Because scaling is externally driven, a plain redeploy uses `maxSurge: 25%, maxUnavailable: 0` so new pods join the
cache mesh before old pods leave — no capacity dip, no double rebalance.

---

## 4. Maintenance

### Scaling behavior
- Bounds are `keda.minReplicas: 10` / `keda.maxReplicas: 100`. The scaler endpoint returns a **desired instance
  count**; with `targetValue: "1"` KEDA sets replicas equal to that estimate, clamped to the bounds.
- Scaling is **deliberately asymmetric** (in `keda.behavior`): fast scale-up (≤10 pods/min), slow capped scale-down
  (≤5 pods/5 min, 10-min stabilization). Rationale: removing many cache members at once triggers rebalancing storms.
- If the scaler endpoint is down `failureThreshold` times, KEDA holds at `keda.fallback.replicas` (20) rather than
  collapsing to the minimum.

### Node drains & upgrades
- A `PodDisruptionBudget` with **fixed `maxUnavailable: 1`** bounds voluntary disruption to a single cache member at
  any fleet size. Node-pool upgrades therefore drain coolkit pods one at a time — slower but safe. `unhealthyPod
  EvictionPolicy: AlwaysAllow` ensures broken pods never wedge a drain.
- Graceful shutdown: `terminationGracePeriodSeconds: 90` + a `preStop` sleep. The app should catch `SIGTERM`, fail
  readiness, hand off cache partitions, drain in-flight HTTP, then exit. Raise the grace period if hand-off needs
  longer.

### Cold starts / cluster formation
- The headless Service uses `publishNotReadyAddresses: true` so a fleet starting from zero can discover peers before
  any pod is Ready (avoids a bootstrap deadlock).

### DB connections
- Each pod opens its own pool. At 100 replicas even a 5-connection pool ⇒ ~500 connections. Keep per-pod pools small
  and size the Cloud SQL tier's `max_connections` accordingly. Alert on connection count vs. the instance limit.

---

## 5. Observability

| Endpoint | Port | Use |
|---|---|---|
| `/healthz` | 2345 | Startup/liveness/readiness probes |
| `/metrics` | 2345 | Prometheus scrape |

- **ServiceMonitor** (default on) scrapes the `metrics` port. It is **skipped gracefully** if the Prometheus Operator
  CRD is absent — check `NOTES.txt` output after install. Its `additionalLabels` **must** match your Prometheus
  `serviceMonitorSelector` (default `release: kube-prometheus-stack`) or scraping silently never happens.
- On clusters without the Operator, set `metrics.annotations.enabled=true` for annotation-based scraping.

### Suggested dashboards / alerts
- **Replica count vs. scaler estimate** — divergence means KEDA isn't tracking (scaler down → check fallback).
- **Cache membership size** vs. pod count — a persistent gap means peer discovery is failing (DNS TTL lag → consider
  `peerDiscovery.mode=k8s-api`).
- **DB connection count** vs. `max_connections`.
- **HTTP 5xx rate** and p99 latency on `:8080`.
- **Pod restarts / OOMKills** — memory pressure on the 4Gi limit.

---

## 6. Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| `helm upgrade` fails: *image.tag is required* | No SHA passed | Pass `--set image.tag=<SHA>` |
| Replicas stuck at fallback (20) | Scaler endpoint unreachable | `kubectl -n production run … curl <scaler url>`; check the scaler |
| Replicas thrash every deploy | Someone set `spec.replicas` / added an HPA | Ensure `keda.enabled=true`; never add a manual HPA |
| Metrics missing in Prometheus | ServiceMonitor selector mismatch or CRD absent | Fix `serviceMonitor.additionalLabels`; check NOTES warning |
| Pods `CrashLoopBackOff` on DB connect | Secret missing / wrong host | Verify `cloudsql.existingSecret` and `cloudsql.host` |
| Cache split-brain / stale members | Discovery not converging | Try `peerDiscovery.mode=k8s-api`; confirm `ports.cache` |
| Slow node drains | Fixed PDB `maxUnavailable: 1` | Expected; schedule maintenance windows |

```sh
kubectl describe scaledobject coolkit -n production      # KEDA status & last scaler value
kubectl logs -n production -l app.kubernetes.io/instance=coolkit --tail=100
kubectl get endpoints coolkit-headless -n production     # peer set the cache should see
```
