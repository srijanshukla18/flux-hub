# flux-hub

Read-only Flux deployment visibility for developers.

## What it is

`flux-hub` is a small Go service with:
- a UI at `/`
- Flux notification ingress at `/webhook`
- a SQLite read model
- GitHub status / PR comments as a bonus output
- optional Slack output

The UI is the main thing.
GitHub/Slack are secondary.

## Current behavior

### Read model
Flux Hub stores two things:
- **watched Flux object state** in SQLite
- **Flux notification events** in SQLite

Watched resources:
- `GitRepository`
- `Kustomization`
- `HelmRelease`

### Session model
Threads are grouped by:
1. commit SHA if derivable
2. revision
3. source reference
4. object fallback

### State model
Current session/object states:
- `ready`
- `reconciling`
- `failed`
- `stalled`
- `observed`
- `unknown`

## Important design decisions

### No auth
No auth guard on the UI by design.
Assumption: internal-only access later via VPN/internal network.

### Read-only only
No reconcile buttons.
No retry buttons.
No remediation.
No write path to cluster resources.

### Lazy shared watches
Flux Hub does **not** start Kubernetes watches at process startup.

Instead:
- first UI request starts one shared watch set
- all viewers share the same watches
- if a watch fails, Flux Hub retries with backoff
- if the UI goes inactive, watches stop after a timeout

Default idle timeout:
- `WATCH_IDLE_TIMEOUT=2m`

### UI refresh behavior
The browser does **not** keep refreshing forever.

Rules:
- hidden tabs do not auto-refresh
- dashboard refresh runs only for visible tabs, every 10s
- session detail refresh runs every 5s only while non-terminal
- terminal session pages stop auto-refresh
- once UI requests stop, backend watches age out and stop too

## Routes
- `/` — dashboard
- `/session?key=...` — deployment thread detail
- `/healthz` — health check
- `/webhook` — Flux notification ingress

## Environment

### Server / storage
- `LISTEN_ADDR` default `:8080`
- `DATABASE_PATH` default `/tmp/flux-hub.db`
- `WATCH_IDLE_TIMEOUT` default `2m`

### GitHub
- `GITHUB_ENABLED` default `true`
- `GITHUB_TOKEN`
- `GITHUB_REPO`
- `GITHUB_API_URL` default `https://api.github.com`
- `GITHUB_STATUS_CONTEXT` default `flux/deployment`
- `GITHUB_PR_COMMENT` default `true`

### Slack
- `SLACK_ENABLED` default `false`
- `SLACK_WEBHOOK_URL`

Dispatch modes:
- `enabled`
- `dry-run`
- `disabled`

## Project layout
- `main.go` — server bootstrap
- `app.go` — app wiring
- `watch_manager.go` — lazy shared watch lifecycle
- `watch.go` — kube config + Flux watches
- `db.go` — SQLite schema/persistence
- `store.go` — session/state projection
- `handlers.go` — UI + webhook handlers
- `ui.templ` — templ UI source
- `ui_templ.go` — generated templ output
- `static/app.css` — UI styling
- `github.go` — GitHub dispatch
- `slack.go` — Slack dispatch
- `deploy/flux-hub.yaml` — deployment + RBAC
- `design.md` — concise design notes

## Local runbook

```bash
colima start fluxhub --cpu 2 --memory 4 --disk 20
export DOCKER_CONTEXT=colima-fluxhub
kind create cluster --name fluxhub
kubectl config use-context kind-fluxhub
flux install
flux check
```

Build and deploy:

```bash
cd ~/code/flux-hub
export DOCKER_CONTEXT=colima-fluxhub

docker build -t flux-hub:dev .
kind load docker-image --name fluxhub flux-hub:dev
kubectl apply -f deploy/flux-hub.yaml
kubectl rollout status deployment/flux-hub -n flux-system
```

Open UI:

```bash
kubectl port-forward -n flux-system svc/flux-hub 8080:8080
```

Visit:

```text
http://localhost:8080/
```

Demo failure:

```bash
kubectl apply -f deploy/flux-demo-public.yaml
flux reconcile kustomization podinfo-bad-path -n flux-demo --with-source --timeout=90s || true
```

## UI development

If you change `ui.templ`:

```bash
cd ~/code/flux-hub
templ generate
gofmt -w *.go
go build ./...
```
