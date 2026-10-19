# syntax=docker/dockerfile:1.7
#
# One image, four static binaries: /app/api (default), /app/worker, /app/migrate, /app/telegram.
#
#   docker build -t socialos-backend .
#   docker run --env-file .env -p 8080:8080 socialos-backend                  # API
#   docker run --env-file .env socialos-backend /app/worker                   # worker
#   docker run --env-file .env socialos-backend /app/migrate up               # migrations
#   docker run --env-file .env socialos-backend /app/telegram set-webhook     # Telegram webhook mode only
#
# The runtime stage is distroless "static:nonroot": no shell, no package manager,
# uid/gid 65532. HEALTHCHECK uses `api healthcheck` because there is no curl.
#
# Multi-arch: the build stage always runs on the builder's own architecture and
# cross-compiles with GOARCH=$TARGETARCH, so `--platform linux/amd64,linux/arm64`
# needs no QEMU emulation (CGO is off, the binaries are fully static).

# go.mod states the minimum Go version (1.26.0); images are built with the latest patch of that line so that they carry
# the current standard-library security fixes. Keep GO_VERSION in .github/workflows/ci.yml on the same minor version.
# Base images are pinned by digest, so a registry or mirror cannot hand a release a different or stale image for the
# tag; Dependabot (docker ecosystem) bumps the digests weekly.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
# Dependencies first so they stay cached until go.mod/go.sum change. The module cache is deliberately
# NOT a cache mount: it must end up in the layer, otherwise CI (which only persists layers, not
# mounts) would download every module again on each source change.
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
ENV CGO_ENABLED=0
# Build identity for GET /version (internal/buildinfo). release.yml passes the tag version (or sha-<7 hex> for main), the
# short commit and the build time; empty values fall back to "dev"/"unknown". Declared after the dependency layers, so a
# new commit only re-runs the compile step.
ARG VERSION=""
ARG COMMIT=""
ARG BUILT_AT=""
RUN --mount=type=cache,target=/root/.cache/go-build \
    set -eu; \
    export GOOS="${TARGETOS}" GOARCH="${TARGETARCH:-$(go env GOARCH)}"; \
    pkg=github.com/socialos/backend/internal/buildinfo; \
    ldflags="-s -w -X ${pkg}.version=${VERSION} -X ${pkg}.commit=${COMMIT} -X ${pkg}.builtAt=${BUILT_AT}"; \
    for cmd in api worker migrate telegram; do \
      go build -trimpath -ldflags="${ldflags}" -o "/out/${cmd}" "./cmd/${cmd}"; \
    done

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS runtime
WORKDIR /app
COPY --from=build /out/api /out/worker /out/migrate /out/telegram /app/
# Migrations are embedded in the binaries; nothing else is needed at runtime.
USER 65532:65532
ENV APP_ENV=production HTTP_ADDR=:8080 LOG_FORMAT=json
EXPOSE 8080 8081
# Applies to the API. The worker and one-shot containers override or disable it in compose
# (the worker serves /health on :8081, see docker-compose.yml).
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 CMD ["/app/api", "healthcheck"]
CMD ["/app/api"]
