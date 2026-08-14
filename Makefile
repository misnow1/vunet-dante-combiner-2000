.PHONY: build build-pi build-pi-arm test generate-check fmt

build:
	mkdir -p bin
	go build -o bin/combiner ./cmd/combiner
	go build -o bin/combiner-status ./cmd/combiner-status

# 64-bit Raspberry Pi OS (lab virgil01 / aarch64, Pi 4/5)
build-pi:
	mkdir -p bin
	GOOS=linux GOARCH=arm64 go build -o bin/combiner-linux-arm64 ./cmd/combiner
	GOOS=linux GOARCH=arm64 go build -o bin/combiner-status-linux-arm64 ./cmd/combiner-status

# 32-bit Raspberry Pi OS (common on Pi 3)
build-pi-arm:
	mkdir -p bin
	GOOS=linux GOARCH=arm GOARM=7 go build -o bin/combiner-linux-arm ./cmd/combiner
	GOOS=linux GOARCH=arm GOARM=7 go build -o bin/combiner-status-linux-arm ./cmd/combiner-status

test:
	go test ./...

generate-check:
	python3 deploy/pi/generate-nftables.py config/site.example.yaml /tmp/combiner-nftables.conf
	python3 deploy/pi/generate-network-config.py config/site.example.yaml /tmp/combiner-net
	@command -v nft >/dev/null && nft -c -f /tmp/combiner-nftables.conf || echo "nft not installed — skipped nft -c"
	@echo "ok: generated /tmp/combiner-nftables.conf and /tmp/combiner-net"

fmt:
	go fmt ./...
