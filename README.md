# coolkit 
Helm chart to deploy **coolkit** — a sample HTTP API service to be scaled on Kubernetes.

| Trait | How the chart handles it |
|-------|--------------------------|
| Dynamic autoscaling | **KEDA `ScaledObject`** using the `metrics-api` scaler |
| Service ports | API on `8080`, health + Prometheus metrics on `2345` |

## Try on local ([k3d](https://k3d.io/)) cluster
```sh
make k3d-up  # creates a local cluster, installs ArgoCD on it, then the full app stack (Keda, Prometheus and Coolkit)
```
Everything below ArgoCD is GitOps-managed — `argocd-register` applies one [root Application](argocd/root.yaml)
that syncs the tree under [argocd/apps/](argocd/apps/) in sync-wave order:
- **ArgoCD** — GitOps controller; `make k3d-up` installs it via Helm (UI at http://argocd.localhost:8081, `admin`/`coolkit`);
- **KEDA** (`keda.sh`) — wave 0, [argocd/apps/keda.yaml](argocd/apps/keda.yaml); provides the CRDs (like `ScaledObject`) and its operator;
- **kube-prometheus-stack** — wave 0, [argocd/apps/prometheus.yaml](argocd/apps/prometheus.yaml); defines the `ServiceMonitor` CRD and Prometheus components;
- **coolkit** — wave 1, the [Application](argocd/apps/coolkit.yaml) syncs the chart in-cluster, after wave 0 is Healthy.

### Validate helm directly
```sh
helm lint helm/ --set image.tag=example

helm template coolkit helm/ --api-versions monitoring.coreos.com/v1 \
  --namespace production --set image.tag=example

# (optional) private ghcr registry secret for imagePullSecrets
kubectl create secret docker-registry ghcr-pull \
  --namespace production \
  --docker-server=ghcr.io \
  --docker-username=octaviobr \
  --docker-password="$GITHUB_TOKEN" # require read:packages scope

helm upgrade --install coolkit ./helm \
  --namespace production --create-namespace \
  -f argocd/values/k3d-alpha.yaml

# Reach it through k3d ingress
curl http://coolkit.localhost:8081/estimate/coolkit

# View Prometheus
kubectl port-forward svc/prometheus-kube-prometheus-prometheus 9090:9090
# View grafana
kubectl port-forward svc/prometheus-grafana 3000:80
# User: "admin", password from:
kubectl get secret prometheus-grafana -o jsonpath='{.data.admin-password}' | base64 -d;
```

**Helm chart** mainly renders the following manifests:<br>
`Deployment` · `Service` (API) · `ScaledObject` (KEDA) · `ServiceMonitor` (prom metrics).<br>
The k3d overlay also renders a Traefik `Ingress` via `additionalManifests`.<br>
DB connection values could be loaded assuming a pre-existing kubernetes secret, set by `cloudsql.existingSecret`.<br>

## Building a sample container image (src)
There's Go HTTP server that implements a getter/setter "estimate" value (integer) on `:8080/estimate/coolkit`, which also print's it's hostname. Also `/healthz` and `/metrics` on `2345` port for observability and operations.
```sh
make docker-build
```

> **Multi-cluster scaling:** [MULTI-CLUSTER](docs/MULTI-CLUSTER.md)

> 🤖 Disclaimer: Claude code (Opus 4.8) assisted me in generating most of this repo. Though every single line of code has been carefully reviewed by me.
