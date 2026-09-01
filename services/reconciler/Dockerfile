# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN GOWORK=off go mod download
COPY services/reconciler ./services/reconciler
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOWORK=off \
    go build -buildvcs=false -trimpath -ldflags="-s -w -buildid=" \
    -o /out/reconciler ./services/reconciler/cmd/reconciler
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOWORK=off \
    go build -buildvcs=false -trimpath -ldflags="-s -w -buildid=" \
    -o /out/member-levels-projection-verifier ./services/reconciler/cmd/member-levels-projection-verifier

FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
COPY --from=build --chown=10001:10001 /out/reconciler /usr/local/bin/mss-shop-reconciler
COPY --from=build --chown=10001:10001 /out/member-levels-projection-verifier /usr/local/bin/mss-shop-member-levels-projection-verifier
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/mss-shop-reconciler"]
