# Multi-stage, cross-compiled: the build stage runs on the runner's native arch and Go
# emits the target arch, so linux/arm64 builds need no QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/website
# The v2 network registry ships in the same image but runs as its own container
# (command /app/openpak-registry; docs/registry.md): its keys never reach the website.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/openpak-registry ./cmd/openpak-registry

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 65532 -h /app app
WORKDIR /app
COPY --from=build /out/app /app/app
COPY --from=build /out/openpak-registry /app/openpak-registry
COPY --from=build /src/web ./web
RUN mkdir -p /app/storage/media && chown -R 65532:65532 /app
# Runs unprivileged: rootless podman maps this uid into the host's subuid range, and every
# listener is >=20000 so no capability is needed to bind.
USER 65532:65532
# CMD, not ENTRYPOINT: the website is the default, and the registry container replaces it
# with its own command instead of passing /app/openpak-registry to the website as an argument.
CMD ["/app/app"]
