# Release image built by GoReleaser's dockers_v2 (DESIGN.md §23.2) from the binary it already
# built, so no Go toolchain runs here. Same image contract as the source-built Dockerfile.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/antispam /antispam
USER nonroot:nonroot
ENTRYPOINT ["/antispam"]
