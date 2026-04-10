FROM golang:1.25 AS build
WORKDIR /src

COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/flux-hub ./cmd/flux-hub

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/flux-hub /flux-hub
EXPOSE 8080
ENTRYPOINT ["/flux-hub"]
