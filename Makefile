.PHONY: verify server-test server-vet server-build server-build-linux-amd64 server-build-linux-arm64 server-build-hosts web-install web-dev web-build clean

GO_CACHE ?= $(CURDIR)/.cache/go-build

verify: server-test server-vet server-build web-build

server-test:
	GOCACHE=$(GO_CACHE) go -C server test ./...

server-vet:
	GOCACHE=$(GO_CACHE) go -C server vet ./...

server-build:
	mkdir -p server/bin
	GOCACHE=$(GO_CACHE) go -C server build -o bin/pam-control ./cmd/pam-control
	GOCACHE=$(GO_CACHE) go -C server build -o bin/pam-connector ./cmd/pam-connector
	GOCACHE=$(GO_CACHE) go -C server build -o bin/pam-operator ./cmd/pam-operator
	GOCACHE=$(GO_CACHE) go -C server build -o bin/pam-linux-connector ./cmd/pam-linux-connector
	GOCACHE=$(GO_CACHE) go -C server build -o bin/pam-network-connector ./cmd/pam-network-connector
	GOCACHE=$(GO_CACHE) go -C server build -o bin/pam-linux-helper ./cmd/pam-linux-helper

server-build-linux-amd64:
	mkdir -p server/bin/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/pam-control ./cmd/pam-control
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/pam-connector ./cmd/pam-connector
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/pam-operator ./cmd/pam-operator
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/pam-linux-connector ./cmd/pam-linux-connector
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/pam-network-connector ./cmd/pam-network-connector
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-amd64/pam-linux-helper ./cmd/pam-linux-helper

server-build-linux-arm64:
	mkdir -p server/bin/linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-arm64/pam-linux-connector ./cmd/pam-linux-connector
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-arm64/pam-network-connector ./cmd/pam-network-connector
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/linux-arm64/pam-linux-helper ./cmd/pam-linux-helper

server-build-hosts:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/windows-amd64/pam-host-connector-windows-amd64.exe ./cmd/pam-host-connector
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/windows-arm64/pam-host-connector-windows-arm64.exe ./cmd/pam-host-connector
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/macos-amd64/pam-host-connector-macos-amd64 ./cmd/pam-host-connector
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 GOCACHE=$(GO_CACHE) go -C server build -trimpath -o bin/macos-arm64/pam-host-connector-macos-arm64 ./cmd/pam-host-connector

web-install:
	npm --prefix web install

web-dev:
	npm --prefix web run dev

web-build:
	npm --prefix web run build

clean:
	rm -rf .cache server/bin web/dist web/*.tsbuildinfo
