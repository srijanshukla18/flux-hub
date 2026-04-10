# flux-hub

Read-only UI for Flux deployment status.

Flux Hub:
- shows focused Flux objects in the browser
- supports direct URLs like `/?kind=HelmRelease&ns=apps&name=api`
- supports PR/commit focus like `/?pr=123` and `/?sha=<commit>`
- uses live Kubernetes reads/watches for current state
- can upsert one sticky GitHub PR comment from Flux webhook events

## URLs

Direct object focus:

```text
/?kind=HelmRelease&ns=apps&name=api
/?kind=Kustomization&ns=flux-system&name=apps
```

PR / commit focus:

```text
/?pr=123
/?sha=<commit-sha>
```

Supported kinds:
- `HelmRelease`
- `Kustomization`
- `GitRepository`

## GitHub

`?pr=` / `?sha=` needs:
- `GITHUB_TOKEN`
- `GITHUB_REPO=owner/repo`

Sticky PR comments are optional and only happen when:
- `GITHUB_ENABLED=true`
- `GITHUB_PR_COMMENT=true`

## Run

```bash
kubectl apply -f deploy/flux-hub.yaml
kubectl rollout status deployment/flux-hub -n flux-system
kubectl port-forward -n flux-system svc/flux-hub 8080:8080
```

Open:

```text
http://localhost:8080/
```

## Config

- `LISTEN_ADDR` default `:8080`
- `DATABASE_PATH` default `/tmp/flux-hub.db`
- `WATCH_IDLE_TIMEOUT` default `2m`
- `GITHUB_TOKEN`
- `GITHUB_REPO`
- `GITHUB_API_URL` default `https://api.github.com`
- `GITHUB_ENABLED` default `false`
- `GITHUB_PR_COMMENT` default `false`
- `FLUX_HUB_URL`

## Notes

- Flux Hub is read-only against cluster resources.
- It does create its own pod/service/RBAC when deployed in-cluster.
- `/webhook` has no auth. Keep it cluster-internal.
- current deploy is single-replica SQLite.

## Docs

- [Design](docs/design.md)
- [Operations](docs/operations.md)
- [Local development](docs/local-dev.md)
