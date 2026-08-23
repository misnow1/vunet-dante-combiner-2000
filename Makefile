.PHONY: build build-pi build-pi-arm build-linux-amd64 package test test-py lint-py fmt fmt-check generate-check check

# Strip leading v from VERSION (e.g. v0.1.0 → 0.1.0). Override: make package VERSION=0.1.0
VERSION ?= $(shell v=$$(git describe --tags --exact-match 2>/dev/null) || v=$$(git describe --tags --always 2>/dev/null) || v=dev; echo $${v#v})
DIST := dist
PACKAGE_PREFIX := vunet-dante-combiner-$(VERSION)
PACKAGE_ARCHS := arm64 arm amd64

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

# x86_64 Linux (lab servers / future non-Pi hosts)
build-linux-amd64:
	mkdir -p bin
	GOOS=linux GOARCH=amd64 go build -o bin/combiner-linux-amd64 ./cmd/combiner
	GOOS=linux GOARCH=amd64 go build -o bin/combiner-status-linux-amd64 ./cmd/combiner-status

# Stage install trees under dist/ and create per-arch tarballs + SHA256SUMS.
package: build-pi build-pi-arm build-linux-amd64
	rm -rf "$(DIST)"
	mkdir -p "$(DIST)"
	@for arch in $(PACKAGE_ARCHS); do \
		stage="$(DIST)/$(PACKAGE_PREFIX)-linux-$$arch"; \
		echo "staging $$stage"; \
		mkdir -p "$$stage/bin" "$$stage/config" "$$stage/deploy/pi"; \
		cp "bin/combiner-linux-$$arch" "$$stage/bin/combiner"; \
		cp "bin/combiner-status-linux-$$arch" "$$stage/bin/combiner-status"; \
		chmod 755 "$$stage/bin/combiner" "$$stage/bin/combiner-status"; \
		cp config/site.example.yaml config/site.tagged-trunk.example.yaml \
			config/site.lab-flat.example.yaml "$$stage/config/"; \
		cp -a config/allowlists "$$stage/config/"; \
		cp deploy/pi/install.sh \
			deploy/pi/generate-nftables.py \
			deploy/pi/generate-nftables.sh \
			deploy/pi/generate-network-config.py \
			deploy/pi/site_config.py \
			deploy/pi/README.md \
			"$$stage/deploy/pi/"; \
		cp -a deploy/pi/systemd "$$stage/deploy/pi/"; \
		chmod 755 "$$stage/deploy/pi/install.sh" \
			"$$stage/deploy/pi/generate-nftables.py" \
			"$$stage/deploy/pi/generate-nftables.sh" \
			"$$stage/deploy/pi/generate-network-config.py"; \
		tar -C "$(DIST)" -czf "$$stage.tar.gz" "$(PACKAGE_PREFIX)-linux-$$arch"; \
		echo "wrote $$stage.tar.gz"; \
	done
	@cd "$(DIST)" && (command -v sha256sum >/dev/null && sha256sum $(PACKAGE_PREFIX)-linux-*.tar.gz || shasum -a 256 $(PACKAGE_PREFIX)-linux-*.tar.gz) > SHA256SUMS
	@echo "ok: packages in $(DIST)/ (VERSION=$(VERSION))"

test:
	go test ./...

test-py:
	python3 -m pytest

lint-py:
	python3 -m ruff check deploy/pi
	python3 -m ruff format --check deploy/pi
	python3 -m mypy

fmt:
	go fmt ./...
	python3 -m ruff format deploy/pi

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:" && gofmt -l . && exit 1)

# Render every shipped profile: audio trunk (default), fully tagged, flat lab.
generate-check:
	@for profile in site.example site.tagged-trunk.example site.lab-flat.example; do \
		echo "generating $$profile"; \
		python3 deploy/pi/generate-nftables.py config/$$profile.yaml /tmp/combiner-nftables-$$profile.conf || exit 1; \
		python3 deploy/pi/generate-network-config.py config/$$profile.yaml /tmp/combiner-net-$$profile >/dev/null || exit 1; \
		if command -v nft >/dev/null; then nft -c -f /tmp/combiner-nftables-$$profile.conf || exit 1; fi; \
	done
	@command -v nft >/dev/null || echo "nft not installed — skipped nft -c"
	@echo "ok: generated /tmp/combiner-nftables-*.conf and /tmp/combiner-net-*"

check: fmt-check test test-py lint-py generate-check build build-pi build-pi-arm build-linux-amd64
