# Operations

## Deploy

```bash
kubectl apply -f deploy/flux-hub.yaml
kubectl rollout status deployment/flux-hub -n flux-system
```

Port-forward for access:

```bash
kubectl port-forward -n flux-system svc/flux-hub 8080:8080
```

## Required cluster capabilities

Flux Hub needs read-only access to:
- Flux objects:
  - `helmreleases`
  - `kustomizations`
  - `gitrepositories`
- workload diagnostics for failed HelmReleases:
  - `pods`
  - `events`
  - `deployments`
  - `statefulsets`
  - `jobs`

Current deploy grants `get/list/watch` only.

## GitHub config

For private-repo PR mode:
- `GITHUB_TOKEN`
- `GITHUB_REPO=owner/repo`

`?pr=` / `?sha=` resolution works even if:
- `GITHUB_ENABLED=false`

Because those query modes only need GitHub read access.

GitHub write actions require:
- `GITHUB_ENABLED=true`

Optional GitHub write settings:
- `GITHUB_STATUS_CONTEXT`
- `GITHUB_PR_COMMENT=true`
- `FLUX_HUB_URL`

## Slack config

Optional:
- `SLACK_ENABLED=true`
- `SLACK_WEBHOOK_URL`

## Flux notifications

The UI works without webhook events.
Webhook events add timeline/history.

Example Flux notification setup:

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

`/webhook` has no auth.
Keep it cluster-internal only.

## Runtime behavior

- no watches at backend startup
- opening a focused UI page starts lazy watches
- hidden tabs do not refresh
- failed HelmRelease pages do a one-shot live diagnostic scan
- diagnostics inspect live objects labeled with:
  - `app.kubernetes.io/instance=<helmrelease-name>`

## Observability

Useful commands:

```bash
kubectl get pods -n flux-system -l app=flux-hub
kubectl logs -n flux-system -l app=flux-hub --tail=100
kubectl describe helmrelease -n <ns> <name>
kubectl get events -n <ns> --sort-by=.lastTimestamp | tail -n 50
```

## Limitations

- single replica only
- default SQLite volume is ephemeral
- PR focus resolves direct `HelmRelease` YAML changes only
- `/webhook` has no auth
- no multi-cluster support
