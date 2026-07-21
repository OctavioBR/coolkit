# coolkit — Future Improvements / Hardening

The default chart keeps things deliberately simple. This document collects the higher-effort hardening steps worth
taking as the service matures, headlined by the Cloud SQL connectivity upgrade.

---

## 1. Cloud SQL Auth Proxy sidecar + Workload Identity (recommended DB hardening)

**Today (default):** the app connects directly to the Cloud SQL private IP using `DB_USER` / `DB_PASSWORD` from a
Kubernetes Secret. Simple, but relies on a long-lived password and VPC-level network reachability.

**Improvement:** run the [Cloud SQL Auth Proxy](https://cloud.google.com/sql/docs/postgres/sql-proxy) as a sidecar
and authenticate with **Workload Identity** — ideally with **IAM database authentication** so there is *no stored
password at all* (the GCP service account identity *is* the credential).

### Why
- Short-lived, identity-based credentials instead of a static password.
- Encrypted connection to the instance without managing certs.
- With `--auto-iam-authn`, the DB Secret disappears entirely.

### What changes in the chart
1. **ServiceAccount annotation** binding the pod to a GCP service account:
   ```yaml
   serviceAccount:
     annotations:
       iam.gke.io/gcp-service-account: coolkit@<project>.iam.gserviceaccount.com
   ```
2. **Proxy sidecar** — run it as a **native sidecar** (an `initContainers` entry with `restartPolicy: Always`, GA in
   Kubernetes 1.29) so it terminates *after* the app finishes draining. Plain sidecars receive `SIGTERM` at the same
   time as the app, which can fail an in-flight DB write during graceful shutdown.
   ```yaml
   initContainers:
     - name: cloud-sql-proxy
       image: gcr.io/cloud-sql-connectors/cloud-sql-proxy:2.x
       restartPolicy: Always          # <- makes it a native sidecar
       args:
         - "--private-ip"
         - "--auto-iam-authn"          # no password needed
         - "--port=5432"
         - "<project>:<region>:<instance>"
       resources: { requests: { cpu: 100m, memory: 128Mi }, limits: { cpu: 250m, memory: 256Mi } }
   ```
3. App connects to `127.0.0.1:5432` instead of the private IP; `DB_HOST` becomes `127.0.0.1` and the password Secret
   is dropped (IAM auth) or kept only as a fallback.

### GCP-side prerequisites (out of scope for the chart)
- A GCP service account with `roles/cloudsql.client` (and `roles/cloudsql.instanceUser` for IAM auth).
- A Workload Identity binding (`roles/iam.workloadIdentityUser`) between the KSA and GSA.
- The DB user provisioned as an IAM user (for `--auto-iam-authn`).

### Trade-off
- Up to 100 proxy processes at max scale, each with its own connection pool. Size `max_connections` accordingly —
  same connection-count ceiling as today, just moved behind the proxy.

---

## 2. Split health endpoints: `/healthz` (liveness) + `/readyz` (readiness)

Currently both liveness and readiness reuse the single `/healthz:2345` endpoint. A dedicated **membership-aware
`/readyz`** (reports "joined the cache cluster and safe to serve") would let readiness gate API traffic on cache
health without risking liveness kills during a transient rebalance. Requires an app change; then point the readiness
probe at `/readyz`.

---

## 3. Kubernetes-API peer discovery by default

`peerDiscovery.mode=k8s-api` (already supported, off by default) has the app watch the API server for peer pods
instead of resolving DNS. It avoids resolver TTL lag during rapid 10→100 scale events, at the cost of a namespaced
RBAC `Role`. Consider making it the default if you observe stale cache membership under bursty scaling.

---

## 4. NetworkPolicy by default

`networkPolicy.enabled=true` restricts ingress to known consumer namespaces + Prometheus + intra-cluster cache
traffic. On a shared cluster this should become the default once the consumer set is enumerated in
`networkPolicy.allowedNamespaces`.

---

## 5. Scaler call authentication

`keda.auth.enabled=true` wires a `TriggerAuthentication` (bearer / basic / TLS) for the metrics-api call. Enable it
once the scaler requires auth — no chart change needed, just a values flip plus an out-of-band Secret.

---

## 6. Autoscaling refinements to revisit with real traffic

- Tune `keda.behavior` scale-down aggressiveness against observed rebalancing cost.
- Reconsider `keda.scaler.targetValue` if the endpoint's response semantics change from "desired count" to a raw
  load metric (then `targetValue` becomes the per-pod capacity divisor).
- Add a `PrometheusRule` for the alerts listed in [OPERATIONS.md](OPERATIONS.md#5-observability).
