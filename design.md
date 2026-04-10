# flux-hub design

## Goal

Answer: did my PR deploy? is it stuck? what's the error?

No dashboard. No overview. No team-wide status page. One PR, one set of HelmReleases, live status.

## Shape

Single Go service: UI + webhook receiver + targeted Kubernetes watches + SQLite read model + optional GitHub/Slack dispatch.

If GitHub is enabled, webhook-driven GitHub updates can also:
- set commit status
- upsert one sticky PR tracking comment with the Flux Hub link

## Usage flow

1. Developer opens `/?pr=123` (or `/?sha=abc1234`)
2. flux-hub calls GitHub to get changed files
3. Parses YAML files for `kind: HelmRelease` → extracts name + namespace
4. Watches only those specific HelmReleases in Kubernetes
5. Page shows live state: READY / RECONCILING / FAILED / STALLED

## Why targeted watches (not namespace-wide)

With 200+ HelmReleases per namespace across multiple teams, watching everything is noisy and expensive. A PR typically touches 1–5 releases. With a `metadata.name=<name>` field selector, the API server streams only that specific object — not the full namespace list.

One informer per targeted release. Clean and minimal.

## Watch lifecycle

- Watches are **lazy** — only start when a PR/SHA is provided
- Idle timeout (`WATCH_IDLE_TIMEOUT`, default 2m) stops watches when UI goes inactive
- Switching PR/SHA cancels the current watch goroutine and restarts with new targets
- Retry with exponential backoff on watch failure

## Source of truth

Two inputs:
- **Kubernetes watches** — current object state, projected into SQLite
- **Flux notification events** — timeline context via `/webhook`

The UI reads the SQLite read model. The watch keeps it warm.

## HelmRelease extraction

Changed YAML files are fetched via GitHub contents API at the PR head SHA. Each file is parsed as multi-document YAML. Any document with `kind: HelmRelease` contributes a `{namespace, name}` pair.

## Design constraints

- **Read-only** — no writes to cluster, no reconcile buttons, no retries
- **No auth** — assumed internal/VPN-only access
- **One page** — no navigation, no sub-pages, no sessions view
- **Terse UI** — minimal copy, no marketing text; errors are shown in full
