# flux-hub design

## Goal

Give developers a simple, read-only way to answer:
- did Flux see my change?
- is it still reconciling?
- did it fail?
- is it stalled?

The UI is the product.
GitHub and Slack are extras.

## Shape

Single Go service.

Contains:
- UI
- Flux webhook receiver
- lazy shared Kubernetes watches
- SQLite read model
- optional GitHub/Slack dispatch

## Source of truth

Two inputs:
- **Flux watches** for current object state
- **Flux notifications** for timeline/history

Watched resources:
- `GitRepository`
- `Kustomization`
- `HelmRelease`

The UI reads the projected SQLite state, not raw cluster objects.

## Why this design

Notifications alone are not enough.
They tell you something happened, but not always what is true right now.

Watches are better for:
- current state
- progress
- stall detection
- session status

Events are still useful for chronology.

## Session model

A session is grouped by:
1. commit SHA
2. revision
3. source reference
4. object fallback

Current states:
- `ready`
- `reconciling`
- `failed`
- `stalled`
- `observed`
- `unknown`

## API server load strategy

Do not watch on backend startup forever.
Do not poll Kubernetes on every UI request.
Do not start one watch per tab.

Use:
- **lazy shared watches**
- **single shared watch set** for all viewers
- **retry with backoff** on watch failure
- **idle timeout shutdown** when UI goes inactive

Current behavior:
- first UI request starts watches
- hidden tabs stop auto-refresh
- dashboard refreshes only while visible
- terminal session pages stop auto-refresh
- backend stops watches after `WATCH_IDLE_TIMEOUT`

This keeps the design simple and avoids unnecessary control plane load.

## Read-only promise

No auth for now.
Assumed internal-only access.

No remediation features:
- no retries
- no reconcile buttons
- no deletes
- no patch/update of Flux objects

RBAC is read-only for watched Flux resources.

## Near-term future work

- better stalled heuristics
- GitHub merge webhook for true `pending` before Flux reacts
- workload enrichment for Pods / Jobs / runtime failures
