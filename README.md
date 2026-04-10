# flux-hub

Track a PR's HelmRelease deployments in real time. Open `/?pr=123`, see which releases are reconciling, ready, failed, or stalled — watching only the specific objects that changed, not everything in the namespace.

---

## How it works

```
Browser ──?pr=123──► handler
                        │
                        ├─► GitHub API: get changed files for PR head SHA
                        │     └─► parse YAML → find kind: HelmRelease
                        │              └─► extract metadata.name + metadata.namespace
                        │
                        ├─► WatchController: start targeted k8s informers
                        │     └─► one informer per release
                        │           field selector: metadata.name=<name>
                        │           namespace-scoped (not cluster-wide)
                        │
                        └─► render page from SQLite read model
                              ↑
                      informers write here on every k8s event
                              ↑
                      Flux also POSTs notification events → /webhook → SQLite
```

Two sources of truth merge in SQLite:
- **Kubernetes watch** — current `status.conditions` of each HelmRelease (live, continuous)
- **Flux notification events** — historical timeline via the `/webhook` endpoint (event-driven)

The UI reads SQLite. It does not call Kubernetes on every page load.

---

## Data flow in detail

### When a user opens `/?pr=123`

1. `focusFromRequest` extracts `pr=123`, calls `resolveHelmReleasesFromPR(123)`.
2. GitHub API: `GET /repos/{owner}/{repo}/pulls/123` → head commit SHA.
3. GitHub API: `GET /repos/{owner}/{repo}/pulls/123/files` → list of changed filenames.
4. For each `.yaml`/`.yml` file that wasn't deleted: `GET /repos/{owner}/{repo}/contents/{path}?ref={sha}` → base64 content.
5. Content is base64-decoded, then parsed as **multi-document YAML**.
   - each YAML document is decoded separately
   - if a document has `kind: HelmRelease` and `metadata.name`, it becomes a watch target
   - `metadata.namespace` is used if present, otherwise the code falls back to `default`
6. Resolution result cached in-memory keyed by `"pr=123"`. Repeated page loads and HTMX refreshes hit the cache — no repeated GitHub API calls.
7. `WatchController.TouchWithFocus("pr=123", releases)` called. Watch starts (or restarts if the union changed).
8. Page rendered from SQLite. If the watch hasn't synced yet, cards show "UNKNOWN".
9. Browser auto-refreshes the status panel every 8 seconds via HTMX (`GET /ui/status?pr=123`).

### Kubernetes watch mechanics

For each resolved `{namespace, name}` pair, one informer is created:

```go
dynamicinformer.NewFilteredDynamicSharedInformerFactory(
    dynamicClient, 0, ref.Namespace,
    func(opts *metav1.ListOptions) {
        opts.FieldSelector = "metadata.name=" + ref.Name
    },
)
```

The field selector pushes the filter to the API server — the watch stream only carries events for that single object, not the whole namespace. With 200+ HelmReleases per namespace, this matters.

HelmRelease GVR is discovered dynamically at watch start via `ServerPreferredResources()`, so the code works with `v2`, `v2beta1`, etc. without hardcoding.

### Multi-user watch union

The `WatchController` maintains a map of all active focus sets:

```
activeFocuses = {
  "pr=123": {releases: [ns-a/rel-a, ns-a/rel-b], lastSeen: T},
  "pr=456": {releases: [ns-b/rel-c], lastSeen: T},
}
```

The Kubernetes watch targets the **union** of all active sets: `[ns-a/rel-a, ns-a/rel-b, ns-b/rel-c]`.

When Dev A opens PR 123 and Dev B opens PR 456, both work concurrently. The watch covers all their releases simultaneously. When a user's tab goes idle past `WATCH_IDLE_TIMEOUT`, their focus entry is evicted; the union shrinks; the watch restarts with fewer targets.

### SQLite schema

Two tables:

**`flux_objects`** — current state of every watched HelmRelease.  
Primary key: `(api_group, kind, namespace, name)`.  
Updated on every k8s informer event (upsert). Deleted when the object is removed from the cluster.  
Key columns: `state`, `revision`, `commit_sha`, `ready_*`, `reconciling_*`, `stalled_*`, `updated_at`.

**`flux_events`** — Flux notification events from the webhook.  
Append-only. Columns: `session_key`, `kind`, `namespace`, `name`, `severity`, `reason`, `message`, `revision`, `commit_sha`, `received_at`.

**Important:** The default deploy uses an `emptyDir` volume for SQLite. The database is ephemeral — it resets on pod restart. Event history is lost. Object state repopulates once the watch reconnects (seconds). If you need persistent history, mount a PVC at `DATABASE_PATH`.

---

## Deploying

### Prerequisites

- Flux v2 installed in the cluster
- A GitHub token with `repo` scope (or `contents:read` + `pull_requests:read` for fine-grained tokens)
- The cluster has the `HelmRelease` CRD (i.e., the helm-controller is running)

### Create the secret

```bash
kubectl create secret generic flux-hub-config \
  --namespace flux-system \
  --from-literal=GITHUB_TOKEN=ghp_... \
  --from-literal=GITHUB_REPO=myorg/myrepo \
  --from-literal=SLACK_WEBHOOK_URL=https://hooks.slack.com/... # optional
```

Then reference it in `deploy/flux-hub.yaml` under `spec.template.spec.containers[0].envFrom`:

```yaml
envFrom:
  - secretRef:
      name: flux-hub-config
```

### Apply

```bash
kubectl apply -f deploy/flux-hub.yaml
kubectl rollout status deployment/flux-hub -n flux-system
```

### Verify

```bash
kubectl get pods -n flux-system -l app=flux-hub
kubectl logs -n flux-system -l app=flux-hub --tail=50

# open the UI
kubectl port-forward -n flux-system svc/flux-hub 8080:8080
# http://localhost:8080/?pr=123
```

### RBAC

`deploy/flux-hub.yaml` creates a `ClusterRole` with read-only access to:
- `helm.toolkit.fluxcd.io/helmreleases`
- `source.toolkit.fluxcd.io/gitrepositories`
- `kustomize.toolkit.fluxcd.io/kustomizations`

Only `helmreleases` are actively watched in focused mode (the others are discovered but not used). The broader RBAC is there for forwards compatibility. Everything is `get/list/watch` — no write verbs.

### Replicas

**Run exactly one replica.** The SQLite database is local to the pod. Multiple replicas would each have a separate database and separate watches — users would see inconsistent state depending on which pod they hit. For HA, use an external SQLite or switch to Postgres. For now: 1 replica, rely on Kubernetes pod restart for recovery.

---

## Configuring Flux notifications

Without the webhook, flux-hub still works (it shows live state from watches). The webhook adds historical events to the timeline. To enable it, create a Flux `Provider` and `Alert`:

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
  eventSeverity: info   # captures both info and error
  eventSources:
    - kind: HelmRelease
      name: "*"          # all HelmReleases; scope with namespace or matchLabels if needed
```

The webhook endpoint has **no authentication**. It should only be reachable from within the cluster (the `address` above uses the cluster-internal DNS name). Do not expose `/webhook` externally.

Verify notifications are firing:

```bash
kubectl describe alert flux-hub -n flux-system
kubectl get events -n flux-system --field-selector reason=ReconciliationSucceeded
kubectl logs -n flux-system -l app=flux-hub | grep "flux event received"
```

---

## Configuration reference

| Variable | Default | Notes |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | TCP address the HTTP server binds to |
| `DATABASE_PATH` | `/tmp/flux-hub.db` | SQLite file path. Use `/data/flux-hub.db` in-cluster with the emptyDir mount |
| `WATCH_IDLE_TIMEOUT` | `2m` | How long a focus entry stays alive after the last UI refresh. Go duration string. |
| `GITHUB_ENABLED` | `false` | Set to `true` to enable GitHub commit status posting and PR comments |
| `GITHUB_TOKEN` | — | Required for PR/SHA resolution and GitHub dispatch. PAT with `repo` scope. |
| `GITHUB_REPO` | — | `owner/repo` — the repository whose PRs and commits you're tracking |

`?pr=` / `?sha=` resolution works with `GITHUB_ENABLED=false` as long as `GITHUB_TOKEN` and `GITHUB_REPO` are set. `GITHUB_ENABLED` only controls GitHub write actions (statuses/comments).
| `GITHUB_API_URL` | `https://api.github.com` | Override for GitHub Enterprise |
| `GITHUB_STATUS_CONTEXT` | `flux/deployment` | The context string on GitHub commit statuses |
| `GITHUB_PR_COMMENT` | `false` | Set to `true` to upsert one sticky tracking PR comment when a Flux event arrives via webhook |
| `FLUX_HUB_URL` | — | Base URL for the generated PR tracking link in the sticky comment, e.g. `https://flux-hub.internal` |
| `SLACK_ENABLED` | `false` | Set to `true` to enable Slack dispatch on webhook events |
| `SLACK_WEBHOOK_URL` | — | Slack incoming webhook URL |

`WATCH_IDLE_TIMEOUT` controls the per-focus idle eviction. Each HTMX auto-refresh (every 8s on visible tabs) resets the timer. A user who closes the tab stops refreshing; after `WATCH_IDLE_TIMEOUT` their focus entry is evicted. If all users are idle, the watch stops entirely.

---

## Observability

### Log patterns to know

```
# Watch connected — what it's tracking
flux watches connected: 3 HelmReleases: production/payments, production/auth, staging/payments

# Focus resolution — GitHub API calls on first load
focus: fetch file helm/production/payments/release.yaml@abc1234: ...

# Webhook event received — Flux notification arrived
flux event received: {"kind":"HelmRelease","name":"payments","namespace":"production",...}

# Watch restart — focus union changed (new user or different PR)
watch controller error: ...   # if something went wrong before restart

# Idle eviction — a focus timed out
# (no log line; watch silently stops if union becomes empty)
```

### Status dot

The small dot in the top-right of the UI:
- **Green (pulsing)** — connected, watches are running
- **Amber** — connecting or retrying after error
- **Red** — watch is in a degraded/error state
- **Grey** — idle, no active focus

Hover over the dot for the exact status string.

### Inspecting SQLite directly

```bash
kubectl exec -n flux-system deploy/flux-hub -- /flux-hub   # won't work (distroless)
# Instead, copy the db out:
kubectl cp flux-system/$(kubectl get pod -n flux-system -l app=flux-hub -o name | head -1 | cut -d/ -f2):/data/flux-hub.db /tmp/flux-hub.db

sqlite3 /tmp/flux-hub.db "SELECT namespace, name, state, updated_at FROM flux_objects ORDER BY updated_at DESC LIMIT 20;"
sqlite3 /tmp/flux-hub.db "SELECT session_key, kind, name, severity, reason, received_at FROM flux_events ORDER BY received_at DESC LIMIT 20;"
```

---

## Failure modes

### "No HelmRelease manifests found in changed files"

The PR has YAML changes but none of them contain `kind: HelmRelease`. Either:
- The changed files aren't HelmRelease manifests (config maps, ingress, etc.)
- The files were deleted (deleted files are skipped)
- The YAML is malformed enough that parsing fails

No watches start. No data shown.

### "GitHub token and repo are required"

`GITHUB_TOKEN` or `GITHUB_REPO` env var is missing. Set them and restart the pod. Focus resolution is in-memory cached — the cache is per-pod and clears on restart.

### "HelmRelease CRD not found in cluster"

`helm-controller` isn't installed or its CRDs aren't registered. Run `flux check` to verify.

### Watch shows "degraded"

The Kubernetes API server is unreachable or returned an error. flux-hub retries with exponential backoff (1s → 2s → 4s → … → 30s cap). Check:

```bash
kubectl logs -n flux-system -l app=flux-hub | grep "watch controller error"
kubectl auth can-i list helmreleases --as=system:serviceaccount:flux-system:flux-hub
```

### HelmRelease shows UNKNOWN

The watch hasn't received an event for that object yet. This is normal for the first few seconds after opening a PR. It can also mean the HelmRelease doesn't exist in the namespace specified in the YAML manifest's `metadata.namespace`.

### Sticky PR comment

When all of these are true:
- `GITHUB_ENABLED=true`
- `GITHUB_PR_COMMENT=true`
- `GITHUB_TOKEN` and `GITHUB_REPO` are set
- a Flux webhook event resolves to one or more PRs for the commit SHA

Flux Hub will upsert **one sticky PR comment** per PR.
It reuses the same comment by looking for an internal marker, then updates it with:
- the Flux Hub tracking link: `FLUX_HUB_URL/?pr=<number>`
- the latest Flux signal summary
- the latest Flux message

### GitHub API rate limiting

Authenticated requests: 5,000/hr (per token). Each PR resolution costs:
- 1 request for PR info (head SHA)
- 1 request for PR files list
- N requests for file content (one per changed YAML file)

Results are cached in-memory per PR/SHA. Rate limiting only triggers if many different PRs/SHAs are being resolved simultaneously, or if the pod restarts frequently (cache cleared on restart). For GitHub Enterprise, set `GITHUB_API_URL`.

### Webhook events not arriving

```bash
# Check Flux Alert is ready
kubectl describe alert flux-hub -n flux-system | grep -A5 "Conditions"

# Check Provider address is correct
kubectl describe provider flux-hub -n flux-system | grep address

# Manually test the webhook
kubectl run --rm -it curl --image=curlimages/curl --restart=Never -- \
  curl -s -X POST http://flux-hub.flux-system.svc.cluster.local:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{"involvedObject":{"kind":"HelmRelease","name":"test","namespace":"default"},"severity":"info","reason":"ReconciliationSucceeded","message":"test event","reportingController":"helm-controller","timestamp":"2024-01-01T00:00:00Z"}'
```

---

## Limitations

| Limitation | Detail |
|---|---|
| Single replica only | SQLite is local; multiple replicas split state |
| Ephemeral event history | Default deploy uses `emptyDir` — event history lost on pod restart. Mount a PVC for persistence. |
| In-memory focus cache | PR→HelmRelease resolution cache clears on pod restart. First load after restart re-calls GitHub API. |
| HelmRelease only | Focused watch targets only HelmReleases. GitRepository or Kustomization failures won't appear unless they manifest as a HelmRelease condition. |
| No webhook auth | `/webhook` trusts all POST requests. Keep it cluster-internal only. |
| GitHub pagination | PR file list is capped at 100 files per page. PRs touching >100 YAML files may miss some. |
| No multi-cluster | One flux-hub instance watches one cluster (the one it's running in, via in-cluster config or `KUBECONFIG`). |

---

## Local development

### Setup

```bash
colima start fluxhub --cpu 2 --memory 4 --disk 20
export DOCKER_CONTEXT=colima-fluxhub
kind create cluster --name fluxhub
kubectl config use-context kind-fluxhub
flux install
flux check
```

### Build and deploy

```bash
docker build -t flux-hub:dev .
kind load docker-image --name fluxhub flux-hub:dev
kubectl apply -f deploy/flux-hub.yaml
kubectl rollout status deployment/flux-hub -n flux-system
```

### Port-forward and test

```bash
kubectl port-forward -n flux-system svc/flux-hub 8080:8080

# UI
open http://localhost:8080/?pr=123

# Health
curl http://localhost:8080/healthz

# Inject a test webhook event
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{
    "involvedObject": {"kind":"HelmRelease","name":"my-release","namespace":"production"},
    "severity": "error",
    "reason": "ChartPullFailed",
    "message": "failed to pull chart: connection refused",
    "reportingController": "helm-controller",
    "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'"
  }'
```

### Run locally (no cluster)

```bash
export GITHUB_TOKEN=ghp_...
export GITHUB_REPO=myorg/myrepo
export DATABASE_PATH=/tmp/flux-hub.db
go run .
# open http://localhost:8080/?pr=123
# watch panel shows UNKNOWN (no k8s) but resolution and UI work
```

---

## Building

```bash
# Binary
go build -o flux-hub .

# Docker image
docker build -t flux-hub:latest .

# After editing ui.templ, regenerate the Go output before building
go run github.com/a-h/templ/cmd/templ@v0.3.1001 generate
go build ./...
```

---

## Code map

| File | What it does |
|---|---|
| `main.go` | HTTP server setup, route registration |
| `app.go` | App struct, startup, in-memory focus resolution cache |
| `config.go` | Env var loading for GitHub and Slack config |
| `watch_manager.go` | Lazy watch lifecycle: per-focus-key activity tracking, union computation, idle eviction, goroutine restart on focus change |
| `watch.go` | Kubernetes client setup, CRD discovery, one informer per focused HelmRelease, SQLite upsert/delete on watch events |
| `github.go` | PR/commit file resolution, HelmRelease YAML extraction, GitHub commit status posting, sticky PR comment upsert |
| `db.go` | SQLite schema (`flux_objects`, `flux_events`), all read/write queries |
| `store.go` | View model projection: filters objects by focus, builds `TargetViewModel` and `EventRowViewModel` |
| `handlers.go` | HTTP handlers: focus resolution from query params, rendering, webhook ingress |
| `slack.go` | Slack webhook dispatch on notification events |
| `ui.templ` | UI template source (templ DSL) |
| `ui_templ.go` | Generated Go from `ui.templ` — do not edit directly |
| `ui_helpers.go` | Template helper functions |
| `util.go` | Shared utilities: env parsing, string helpers, request logger |
| `static/app.css` | Dark UI stylesheet |
| `deploy/flux-hub.yaml` | ServiceAccount, ClusterRole, ClusterRoleBinding, Deployment, Service |
