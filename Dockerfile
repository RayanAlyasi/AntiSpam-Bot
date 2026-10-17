# Multi-arch build of the core image (DESIGN.md §19.2): pure Go, CGO_ENABLED=0, distroless.
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/antispam ./cmd/antispam

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/antispam /antispam
USER nonroot:nonroot
ENTRYPOINT ["/antispam"]
