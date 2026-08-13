.PHONY: build build-pi test generate-check fmt

build:
	mkdir -p bin
	go build -o bin/combiner ./cmd/combiner
	go build -o bin/combiner-status ./cmd/combiner-status

build-pi:
	mkdir -p bin
	GOOS=linux GOARCH=arm64 go build -o bin/combiner-linux-arm64 ./cmd/combiner
	GOOS=linux GOARCH=arm64 go build -o bin/combiner-status-linux-arm64 ./cmd/combiner-status

test:
	go test ./...

generate-check:
	python3 deploy/pi/generate-nftables.py config/site.example.yaml /tmp/combiner-nftables.conf
	python3 deploy/pi/generate-network-config.py config/site.example.yaml /tmp/combiner-net
	@command -v nft >/dev/null && nft -c -f /tmp/combiner-nftables.conf || echo "nft not installed — skipped nft -c"
	@echo "ok: generated /tmp/combiner-nftables.conf and /tmp/combiner-net"

fmt:
	go fmt ./...
