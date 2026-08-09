# Build the controller, then ship it on a minimal base.
#
# The build stage is pinned by digest-free tag so it can be mirrored into a
# private registry; override with --build-arg for an on-prem base image:
#   docker build --build-arg GO_IMAGE=registry.internal/golang:1.24 .
ARG GO_IMAGE=golang:1.24
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

FROM ${GO_IMAGE} AS build
WORKDIR /src

# Dependencies first, so a source-only change reuses the module layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Stamped at link time so `netbox-gitops --version` reports something real.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
      -o /out/netbox-gitops ./cmd/netbox-gitops/ \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/yamlcheck ./cmd/yamlcheck/

FROM ${RUNTIME_IMAGE}
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
# Standard OCI annotations, so a registry UI and any scanning in the pipeline
# can identify the image and its licence without unpacking it.
LABEL org.opencontainers.image.title="netbox-gitops-controller" \
      org.opencontainers.image.description="Declarative GitOps controller for NetBox" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"
COPY --from=build /out/netbox-gitops /out/yamlcheck /usr/local/bin/
# Definitions and inventory are mounted in; --data-dir defaults to the CWD.
WORKDIR /data
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/netbox-gitops"]
