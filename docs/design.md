# Design

Flux Hub is a single Go service with:
- HTTP UI
- `/webhook` for Flux notification events
- lazy Kubernetes watches for focused Flux objects
- SQLite read model
- optional sticky GitHub PR comments

## Focus modes

- direct object:
  - `/?kind=HelmRelease&ns=apps&name=api`
- PR:
  - `/?pr=123`
- commit:
  - `/?sha=<commit>`

## Current behavior

- direct object pages read the object immediately from Kubernetes on first load
- watches start lazily when a focused page is open
- hidden tabs do not refresh
- visible pages refresh every 8s
- failed HelmRelease pages run a one-shot diagnostic scan

## Data sources

- Kubernetes object status for current truth
- Flux webhook events for timeline/history
- GitHub API for PR/commit focus resolution

## Diagnostics

For failed HelmReleases, Flux Hub tries to surface the first useful clue:
- failed hook/job
- rollout gap on Deployment or StatefulSet
- bad pod state
- recent warning events
- otherwise a short fallback summary

## Constraints

- read-only against cluster workloads and Flux objects
- no retries or reconcile actions
- no auth built in
- single-replica SQLite deploy
