# coolkit — Scaling from One Cluster to Many

This document outlines how to grow coolkit from a single-cluster deployment to a fleet spanning many clusters /
regions, using **ArgoCD** (the team's existing deployment tool). The concrete manifest lives at
[`../argocd/applicationset.yaml`](../argocd/applicationset.yaml).

---

## The model: one chart, an ApplicationSet, per-region value overlays

A single Git repo is the source of truth. An **ArgoCD `ApplicationSet` with the `clusters` generator** stamps out
one `Application` per registered cluster, each installing the *same* coolkit chart with a region-specific values
file layered on top.

```
                       ┌──────────────────── Git repo (source of truth) ────────────────────┐
                       │  coolkit/ (Helm chart)   argocd/applicationset.yaml   argocd/values/*.yaml │
                       └───────────────────────────────┬────────────────────────────────────┘
                                                        │  ArgoCD ApplicationSet (clusters generator)
                    ┌───────────────────────────────────┼───────────────────────────────────┐
                    ▼                                    ▼                                     ▼
        Application: coolkit-us-central1     Application: coolkit-europe-west1     Application: coolkit-<region>
        values/us-central1.yaml              values/europe-west1.yaml             values/<region>.yaml
                    │                                    │                                     │
              GKE cluster us-central1            GKE cluster europe-west1              GKE cluster <region>
              (own cache ring, own scaler,        (own cache ring, own scaler,          …
               own Cloud SQL)                       own Cloud SQL)
```

**Adding a region** = register the cluster with ArgoCD (labeled `region: <r>`, `env: prod`) + add one
`argocd/values/<region>.yaml`. No per-cluster copy-paste of manifests.

---

## What is genuinely per-region (and what is not)

Only a few values legitimately differ per region — everything else inherits chart defaults:

| Value | Per-region? | Why |
|---|---|---|
| `cloudsql.host` | **Yes** | Cloud SQL is a regional instance / read replica |
| `cloudsql.existingSecret` | **Yes** | Region-local DB credentials |
| `image.tag` | Per-wave | Promote a SHA region-by-region (see rollout below) |
| `keda.scaler.url` | **No** | It's a *cluster-local* DNS name — identical everywhere (see below) |
| `ports`, `resources`, `keda` bounds/behavior, probes, PDB | **No** | Same everywhere |

### Three per-cluster realities to keep in mind

1. **The in-memory cache is per-cluster.** Kubernetes Services don't span clusters, so each region runs an
   **independent cache ring** — there is no cross-cluster cache coherence by design. If a global shared cache is ever
   required, that's a different distributed-systems problem (a shared backing store / global cache tier), not this
   chart.
2. **The KEDA scaler stays per-cluster.** The URL `scaler.production.svc.cluster.local` resolves *within* each
   cluster, so if every cluster runs its own scaler the value needs **zero override**. Avoid a single global scaler:
   it would put cross-region latency and availability on the hot path of the autoscaling loop. Run the scaler
   per-region; keep the URL identical everywhere.
3. **Cloud SQL is per-region.** Each cluster connects to its own regional instance — no cross-region DB traffic
   assumed.

---

## Progressive rollout — canary region first

Use the ApplicationSet's native **`RollingSync`** strategy (ArgoCD ≥ 2.6) so a bad SHA rolls out region-by-region
instead of everywhere at once. Label clusters by wave and order the steps:

- **Step 1 (canary):** one region (e.g. `us-central1`, `maxUpdate: 1`) syncs and bakes.
- **Step 2:** the remaining regions sync once the canary is healthy.

This gives cross-cluster sequencing natively. Note this is *different* from Argo Rollouts, which does **in-cluster**
canary (traffic splitting between pod versions) — a complementary but separate concern we don't need here.

---

## Platform prerequisites per cluster

Cluster-level dependencies (**KEDA operator**, **Prometheus Operator CRDs**) must be installed on a newly onboarded
cluster **before** coolkit syncs. Treat that as a prior **platform-bootstrap App-of-Apps** wave — not something the
coolkit chart enforces itself. The chart only guards defensively: the `ServiceMonitor` is skipped if the Operator CRD
is absent (`.Capabilities.APIVersions.Has`).

---

## Out of scope / adjacent

- **Cross-region traffic routing** (Multi-Cluster Services, a global L7 load balancer) — coolkit is an internal API;
  routing external/global callers to the nearest region is a separate networking design.
- **Global metrics federation** (Thanos / Mimir) — if you aggregate dashboards across regions, add a `region` label
  so identically-named resources don't collide in a global view.

See [`../argocd/applicationset.yaml`](../argocd/applicationset.yaml) and
[`../argocd/values/`](../argocd/values/) for the working manifest and example overlays.
