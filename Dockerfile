# syntax=docker/dockerfile:1.7
#
# Multi-stage, multi-arch build of the `disco` CLI. Output is a static,
# non-root, distroless image suitable for ECS Fargate scan workers.
#
# An external ECS task definition / orchestrator spawns this image and
# overrides the command at run-task time (e.g. `disco scan aws --regions
# us-east-2`).
#
# Usage:
#   docker buildx build \
#     --platform linux/arm64 \
#     --build-arg TARGETOS=linux --build-arg TARGETARCH=arm64 \
#     --build-arg VERSION="$(git describe --tags --always --dirty=+dirty)" \
#     -t disco/scanner:dev .

# Pin the PATCH, not the minor: the official images set GOTOOLCHAIN=local, so
# the builder cannot download a newer toolchain and `go mod download` refuses
# outright when go.mod's `go` directive is above the image's own version. The
# floating `1.25` tag does not save you -- it resolves inside whatever alpine
# variant is named, and a discontinued variant freezes it (1.25-alpine3.21 is
# still Go 1.25.5, eight patches behind this go.mod).
ARG GO_VERSION=1.25.13
# The alpine variant is not a free choice: golang publishes only the alpine
# releases current at the time of each patch, so a GO_VERSION bump can delete
# the variant this pinned, and the deletion persists across later patches. Check
# the tag exists before bumping GO_VERSION.
ARG ALPINE_VERSION=3.23

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder
WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Cross-compile to the target platform. BuildKit injects TARGETOS +
# TARGETARCH automatically when --platform is set on the build invocation.
# VERSION stamps cmd.Version (single -ldflags so -X composes with -s -w; the
# repeated-flag GOFLAGS form kept only the last -ldflags). The build context has
# no .git, so without this the ReadBuildInfo() fallback reports "dev" — and
# `disco --version`, SARIF tool.driver.version, and snapshot tool_version with it.
ARG TARGETOS=linux
ARG TARGETARCH=arm64
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w -X github.com/icearp/disco-cli/cmd.Version=${VERSION}" \
      -o /out/disco .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/disco /usr/local/bin/disco

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/disco"]
CMD ["--help"]
