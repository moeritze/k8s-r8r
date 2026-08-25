# Build the manager binary
FROM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build
# the GOARCH has no default value to allow the binary to be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot

# Static OCI labels. The release workflow additionally injects the dynamic ones
# (org.opencontainers.image.version / .revision / .created) via
# docker/metadata-action, which override anything set here.
# org.opencontainers.image.source is what links the ghcr package page back to
# this repository (source link + README on the package page).
LABEL org.opencontainers.image.title="k8s-r8r" \
      org.opencontainers.image.description="Kubernetes operator for policy-gated cross-cluster replication of Secrets and ConfigMaps" \
      org.opencontainers.image.source="https://github.com/moeritze/k8s-r8r" \
      org.opencontainers.image.url="https://github.com/moeritze/k8s-r8r" \
      org.opencontainers.image.documentation="https://github.com/moeritze/k8s-r8r/blob/main/README.md" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="moeritze"

WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
