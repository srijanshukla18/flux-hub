# Local development

## Prereqs

- Colima
- kind
- kubectl
- flux CLI
- Docker build working against your local runtime

## Local cluster setup

```bash
colima start fluxhub --cpu 2 --memory 4 --disk 20
export DOCKER_CONTEXT=colima-fluxhub
kind create cluster --name fluxhub
kubectl config use-context kind-fluxhub
flux install
flux check
```

## Build and deploy

```bash
docker build -t flux-hub:dev .
kind load docker-image --name fluxhub flux-hub:dev
kubectl apply -f deploy/flux-hub.yaml
kubectl rollout status deployment/flux-hub -n flux-system
```

## Open the UI

```bash
kubectl port-forward -n flux-system svc/flux-hub 8080:8080
```

Examples:

```text
http://localhost:8080/
http://localhost:8080/?kind=Kustomization&ns=flux-demo&name=podinfo-bad-path
http://localhost:8080/?kind=HelmRelease&ns=flux-demo&name=redis-git-upgrade-demo
```

## Running locally on your machine

```bash
export DATABASE_PATH=/tmp/flux-hub.db
export GITHUB_TOKEN=ghp_...
export GITHUB_REPO=myorg/myrepo
go run ./cmd/flux-hub
```

## Useful dev commands

```bash
gofmt -w cmd/flux-hub/main.go internal/app/*.go
(cd internal/app && templ generate)
go build ./...
```

## Demo manifests

In `deploy/`:
- `flux-demo-public.yaml`
- `flux-demo-helmrelease-bad.yaml`
- `flux-hub.yaml`

## Troubleshooting

### `?pr=` says token/repo are required
Set:
- `GITHUB_TOKEN`
- `GITHUB_REPO`

### UI shows UNKNOWN
The watch may not have synced yet, or the object may not exist in that namespace.

### Failed HelmRelease diagnostics show nothing useful
Check that the rendered workloads are labeled with:
- `app.kubernetes.io/instance=<helmrelease-name>`

The current simple diagnostic pass depends on that label.
