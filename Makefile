.PHONY: init-local verify server-test server-vet server-build server-build-linux-amd64 server-build-linux-arm64 server-build-hosts web-install web-dev web-lint web-test web-build clean

GO_CACHE ?= $(CURDIR)/.cache/go-build

init-local:
	scripts/init-local-secrets.sh

verify: server-test server-vet server-build web-lint web-test web-build

server-test:
	GOCACHE=$(GO_CACHE) go -C server test ./...

server-vet:
	GOCACHE=$(GO_CACHE) go -C server vet ./...

server-build:
	mkdir -p server/bin
	GOCACHE=$(GO_CACHE) go -C server build -o bin/warden-control ./cmd/warden-control
	GOCACHE=$(GO_CACHE) go -C server build -o bin/warden-connector ./cmd/warden-connector
	GOCACHE=$(GO_CACHE) go -C server build -o bin/warden-operator ./cmd/warden-operator
	GOCACHE=$(GO_CACHE) go -C server build -o bin/warden-linux-connector ./cmd/warden-linux-connector
	GOCACHE=$(GO_CACHE) go -C server build -o bin/warden-network-connector ./cmd/warden-network-connector
	GOCACHE=$(GO_CACHE) go -C server build -o bin/warden-linux-helper ./cmd/warden-linux-helper

server-build-linux-amd64:
	mkdir -p server/bin/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/warden-control ./cmd/warden-control
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/warden-connector ./cmd/warden-connector
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/warden-operator ./cmd/warden-operator
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/warden-linux-connector ./cmd/warden-linux-connector
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/warden-network-connector ./cmd/warden-network-connector
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/warden-linux-helper ./cmd/warden-linux-helper

server-build-linux-arm64:
	mkdir -p server/bin/linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-arm64/warden-linux-connector ./cmd/warden-linux-connector
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-arm64/warden-network-connector ./cmd/warden-network-connector
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-arm64/warden-linux-helper ./cmd/warden-linux-helper

server-build-hosts:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/windows-amd64/warden-host-connector-windows-amd64.exe ./cmd/warden-host-connector
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/windows-arm64/warden-host-connector-windows-arm64.exe ./cmd/warden-host-connector
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/macos-amd64/warden-host-connector-macos-amd64 ./cmd/warden-host-connector
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/macos-arm64/warden-host-connector-macos-arm64 ./cmd/warden-host-connector

web-install:
	npm --prefix web install

web-dev:
	npm --prefix web run dev

web-lint:
	npm --prefix web run lint

web-test:
	npm --prefix web run test

web-build:
	npm --prefix web run build

clean:
	rm -rf .cache server/bin web/dist web/*.tsbuildinfo
