# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN GOWORK=off go mod download
COPY services/internal/legacyreceipt ./services/internal/legacyreceipt
COPY services/legacy-importer ./services/legacy-importer
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOWORK=off \
    go build -buildvcs=false -trimpath -ldflags="-s -w -buildid=" \
    -o /out/legacy-importer ./services/legacy-importer/cmd/legacy-importer
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOWORK=off \
    go build -buildvcs=false -trimpath -ldflags="-s -w -buildid=" \
    -o /out/legacy-readiness ./services/legacy-importer/cmd/legacy-import-readiness
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOWORK=off \
    go build -buildvcs=false -trimpath -ldflags="-s -w -buildid=" \
    -o /out/legacy-verifier ./services/legacy-importer/cmd/legacy-import-verifier

FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
COPY --from=build --chown=10001:10001 /out/legacy-importer /usr/local/bin/mss-shop-legacy-importer
COPY --from=build --chown=10001:10001 /out/legacy-readiness /usr/local/bin/mss-shop-legacy-readiness
COPY --from=build --chown=10001:10001 /out/legacy-verifier /usr/local/bin/mss-shop-legacy-verifier
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/mss-shop-legacy-importer"]
