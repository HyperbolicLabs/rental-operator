# Build the manager binary
FROM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH
# VERSION is stamped into the binary and the User-Agent of its API requests.
ARG VERSION=dev

WORKDIR /workspace
# github.com/HyperbolicLabs modules are private for now, so fetch them
# directly instead of through the public module proxy and checksum database.
ENV GOPRIVATE=github.com/HyperbolicLabs/*
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer.
# A read-only GitHub token for the private modules can be passed as the build
# secret "gh_token" (make docker-build does this when HYPERBOLIC_GO_TOKEN is
# set). It is used only by this step and never written to a layer.
RUN --mount=type=secret,id=gh_token,required=false \
    if [ -s /run/secrets/gh_token ]; then \
      export GIT_CONFIG_COUNT=1 \
        GIT_CONFIG_KEY_0="url.https://x-access-token:$(cat /run/secrets/gh_token)@github.com/HyperbolicLabs/.insteadOf" \
        GIT_CONFIG_VALUE_0="https://github.com/HyperbolicLabs/"; \
    fi; \
    go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build
# the GOARCH has no default value to allow the binary to be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a \
    -ldflags "-X github.com/HyperbolicLabs/rental-operator/internal/version.Version=${VERSION}" \
    -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
