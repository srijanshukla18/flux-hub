# Operations

## Deploy

```bash
kubectl apply -f deploy/flux-hub.yaml
kubectl rollout status deployment/flux-hub -n flux-system
kubectl port-forward -n flux-system svc/flux-hub 8080:8080
```

## Cluster access

Flux Hub needs read-only access to:
- `helmreleases`
- `kustomizations`
- `gitrepositories`
- `pods`
- `events`
- `deployments`
- `statefulsets`
- `jobs`

Current manifest grants `get/list/watch` only.

## GitHub config

For `?pr=` / `?sha=` on a private repo:
- `GITHUB_TOKEN`
- `GITHUB_REPO=owner/repo`

Optional sticky PR comments:
- `GITHUB_ENABLED=true`
- `GITHUB_PR_COMMENT=true`
- `FLUX_HUB_URL`

## Flux notifications

The UI works without webhook events.

`/webhook` only adds event history and enables sticky PR comment updates.

Example Flux notification wiring:

```yaml
apiVersion: notification.toolkit.fluxcd.io/v1beta3
kind: Provider
metadata:
  name: flux-hub
  namespace: flux-system
spec:
  type: generic
  address: http://flux-hub.flux-system.svc.cluster.local:8080/webhook
---
apiVersion: notification.toolkit.fluxcd.io/v1beta3
kind: Alert
metadata:
  name: flux-hub
  namespace: flux-system
spec:
  providerRef:
    name: flux-hub
  eventSeverity: info
  eventSources:
    - kind: HelmRelease
      name: "*"
    - kind: Kustomization
      name: "*"
```

`/webhook` has no auth. Keep it cluster-internal.

## Runtime notes

- watches start only when a focused page is open
- failed HelmRelease pages do one-shot live diagnostics
- current deploy is single-replica SQLite
- default SQLite storage is ephemeral
