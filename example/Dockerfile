# syntax=docker/dockerfile:1
#
# Developer image. It compiles the binary inside the image, so a clone with
# only a container runtime installed produces the same artifact the pipeline
# ships. The release pipeline uses Dockerfile.ci instead, which copies the
# binary the verify run already built and tested.
#
# The two files share the runtime base and the runtime stage verbatim. The
# shared text sits between the region markers below and tools/release asserts
# both files carry it byte for byte, so the developer image and the released
# image differ only in how the binary arrived.

# >>> shared runtime base <<<
# The base is pinned by digest, not by tag. A tag on a base repository can be
# moved by its owner, which turns the base into a mutable input and makes the
# provenance claim unprovable. Renovation of this line is a dependency update
# like any other.
ARG RUNTIME_BASE=gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
# >>> end shared runtime base <<<

ARG BUILDER_BASE=golang@sha256:65b6f280bf050ec5af12716857e8ea8439d694dbba8f31ceeb7630670071f2bb

FROM --platform=${BUILDPLATFORM} ${BUILDER_BASE} AS build

# Cross-compilation targets come from the builder, so one native builder
# produces every architecture in the manifest list without emulation.
ARG TARGETOS
ARG TARGETARCH

# Build metadata. SOURCE_DATE_EPOCH is the commit timestamp, which is what
# makes the file times in the image a function of the commit rather than of
# the moment the build ran.
ARG VERSION=dev
ARG COMMIT=unknown
ARG SOURCE_DATE_EPOCH=0
ARG ASSET_HASH=unknown

# GOTOOLCHAIN=local pins the compiler to the one in the base image. Left at
# its default, the build downloads whatever toolchain go.mod names, which is a
# network fetch inside a build that claims to be reproducible.
ENV CGO_ENABLED=0 \
    GOFLAGS=-mod=readonly \
    GOTOOLCHAIN=local

WORKDIR /src

# The module files are copied first so a source-only change reuses the
# download layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# -trimpath removes the build machine's directory names from the binary and
# -buildid= clears the content-addressed build identifier, which otherwise
# varies with the linker's temporary paths. Together with a fixed build time
# they make two builds of one commit produce identical bytes.
RUN BUILD_TIME=$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
        || date -u -r "${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ) && \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
        -trimpath \
        -buildvcs=false \
        -ldflags "-s -w -buildid= \
            -X github.com/example/reference-service/internal/version.version=${VERSION} \
            -X github.com/example/reference-service/internal/version.commit=${COMMIT} \
            -X github.com/example/reference-service/internal/version.buildTime=${BUILD_TIME} \
            -X github.com/example/reference-service/internal/version.assetHash=${ASSET_HASH}" \
        -o /out/app ./cmd/reference-service

FROM ${RUNTIME_BASE} AS runtime

COPY --from=build --chown=65532:65532 /out/app /app

# >>> shared runtime stage <<<
# The base is the static distroless image: a certificate bundle, a time zone
# database, and passwd entries. There is no shell and no package manager, so
# the most common post-compromise foothold is absent. Debugging happens
# through an ephemeral debug container instead.
#
# 65532 is the nonroot user the base defines. It is written numerically
# because a Kubernetes runAsNonRoot check cannot resolve a user name.
USER 65532:65532
WORKDIR /
EXPOSE 8080
ENTRYPOINT ["/app"]
# >>> end shared runtime stage <<<
