ARG GO_IMAGE=golang:1.26-alpine
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot
FROM ${GO_IMAGE} AS build
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ .
ARG TARGET=pam-control
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${TARGET}
RUN mkdir -p /out/artifacts/server/bin/linux-amd64 /out/artifacts/server/bin/linux-arm64 /out/artifacts/server/bin/windows-amd64 /out/artifacts/server/bin/macos-amd64 /out/artifacts/server/bin/macos-arm64 && \
    if [ "$TARGET" = "pam-control" ]; then \
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/artifacts/server/bin/linux-amd64/pam-linux-connector ./cmd/pam-linux-connector && \
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/artifacts/server/bin/linux-amd64/pam-network-connector ./cmd/pam-network-connector && \
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/artifacts/server/bin/linux-amd64/pam-linux-helper ./cmd/pam-linux-helper && \
      CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /out/artifacts/server/bin/linux-arm64/pam-linux-connector ./cmd/pam-linux-connector && \
      CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /out/artifacts/server/bin/linux-arm64/pam-network-connector ./cmd/pam-network-connector && \
      CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /out/artifacts/server/bin/linux-arm64/pam-linux-helper ./cmd/pam-linux-helper && \
      CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/artifacts/server/bin/windows-amd64/pam-host-connector-windows-amd64.exe ./cmd/pam-host-connector && \
      CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/artifacts/server/bin/macos-amd64/pam-host-connector-macos-amd64 ./cmd/pam-host-connector && \
      CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /out/artifacts/server/bin/macos-arm64/pam-host-connector-macos-arm64 ./cmd/pam-host-connector; \
    fi

FROM ${RUNTIME_IMAGE}
COPY --from=build /out/app /app
# Keep the release self-contained: install-config and download endpoints must
# work in a container without the source repository mounted beside the binary.
COPY deploy/ /app-artifacts/deploy/
COPY server/migrations/ /app-artifacts/server/migrations/
COPY --from=build /out/artifacts/ /app-artifacts/
ENV PAM_ARTIFACT_ROOT=/app-artifacts
ENTRYPOINT ["/app"]
