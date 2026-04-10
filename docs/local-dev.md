# Local development

## Run locally against your kubeconfig

```bash
export DATABASE_PATH=/tmp/flux-hub.db
go run ./cmd/flux-hub
```

If PR mode needs a private repo:

```bash
export GITHUB_TOKEN=...
export GITHUB_REPO=owner/repo
```

## Useful URLs

```text
http://localhost:8080/
http://localhost:8080/?kind=HelmRelease&ns=<ns>&name=<name>
http://localhost:8080/?kind=Kustomization&ns=<ns>&name=<name>
http://localhost:8080/?pr=123
```

## Dev commands

```bash
gofmt -w cmd/flux-hub/main.go internal/app/*.go
(cd internal/app && templ generate)
go build ./...
```
