# flux-hub

Read-only UI for Flux/GitOps deployment failures.

Flux Hub is a small Go service that:
- shows focused Flux object status in a browser
- supports direct URLs like `/?kind=HelmRelease&ns=apps&name=api`
- supports PR-driven focus like `/?pr=123`
- stays read-only: no retries, no reconciles, no cluster writes to existing workloads

## Status

Good fit today for:
- internal/nonprod use
- direct object URLs
- HelmRelease and Kustomization visibility
- lightweight deployment diagnostics for failed HelmReleases

## URL formats

### Direct object focus

```text
/?kind=HelmRelease&ns=flux-demo&name=podinfo
/?kind=Kustomization&ns=flux-demo&name=podinfo-bad-path
```

Supported `kind` values:
- `HelmRelease`
- `Kustomization`
- `GitRepository`

### PR / commit focus

```text
/?pr=123
/?sha=<commit-sha>
```

`?pr=` / `?sha=` resolution requires:
- `GITHUB_TOKEN`
- `GITHUB_REPO=owner/repo`

This still works with `GITHUB_ENABLED=false`.
`GITHUB_ENABLED` only controls GitHub write actions like statuses/comments.

## Quick start

### Run in-cluster

```bash
kubectl apply -f deploy/flux-hub.yaml
kubectl rollout status deployment/flux-hub -n flux-system
kubectl port-forward -n flux-system svc/flux-hub 8080:8080
```

Open:

```text
http://localhost:8080/
```

### Minimum env vars for PR mode on a private repo

```text
GITHUB_TOKEN=<token>
GITHUB_REPO=owner/repo
```

### Keep GitHub/Slack writes disabled

```text
GITHUB_ENABLED=false
SLACK_ENABLED=false
```

## Config

Common env vars:
- `LISTEN_ADDR` default `:8080`
- `DATABASE_PATH` default `/tmp/flux-hub.db`
- `WATCH_IDLE_TIMEOUT` default `2m`
- `GITHUB_ENABLED` default `false`
- `GITHUB_TOKEN`
- `GITHUB_REPO`
- `GITHUB_API_URL` default `https://api.github.com`
- `GITHUB_STATUS_CONTEXT` default `flux/deployment`
- `GITHUB_PR_COMMENT` default `false`
- `FLUX_HUB_URL`
- `SLACK_ENABLED` default `false`
- `SLACK_WEBHOOK_URL`

## Notes

- Flux Hub deploys its own pod/service/RBAC.
- It is read-only against existing Flux objects and workloads.
- It does add Kubernetes API read/watch load while the UI is active.
- `/webhook` has no auth; keep it cluster-internal only.
- current deploy is single-replica SQLite.

## Docs

- [Design](docs/design.md)
- [Operations](docs/operations.md)
- [Local development](docs/local-dev.md)
