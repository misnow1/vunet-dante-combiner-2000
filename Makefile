.PHONY: shellcheck build build-pi build-pi-arm build-linux-amd64 package test test-py lint-py fmt fmt-check generate-check nft-check check

# Version for stamped binaries and package names (e.g. v0.1.0 → 0.1.0).
# Override: make package VERSION=0.1.0
# Strip the leading v with sed rather than $${v#v}: GNU Make 3.81 (still the
# system make on macOS) reads that '#' as a comment and fails to parse. Use
# gmake locally; this just keeps plain `make` working for anyone who doesn't.
VERSION ?= $(shell v=$$(git describe --tags --exact-match 2>/dev/null || git describe --tags --always 2>/dev/null || echo dev); echo "$$v" | sed 's/^v//')
DIST := dist
PACKAGE_PREFIX := vunet-dante-combiner-$(VERSION)
PACKAGE_ARCHS := arm64 arm amd64

# Stamp the version into every binary: a field unit has no toolchain and no
# Internet, so `combiner -version` is the only way to identify what it runs.
LDFLAGS := -X github.com/msnow/vunet-dante-combiner-2000/internal/buildinfo.Version=$(VERSION)
GOBUILD := go build -ldflags "$(LDFLAGS)"

build:
	mkdir -p bin
	$(GOBUILD) -o bin/combiner ./cmd/combiner
	$(GOBUILD) -o bin/combiner-status ./cmd/combiner-status

# 64-bit Raspberry Pi OS (lab virgil01 / aarch64, Pi 4/5)
build-pi:
	mkdir -p bin
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o bin/combiner-linux-arm64 ./cmd/combiner
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o bin/combiner-status-linux-arm64 ./cmd/combiner-status

# 32-bit Raspberry Pi OS (common on Pi 3)
build-pi-arm:
	mkdir -p bin
	GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) -o bin/combiner-linux-arm ./cmd/combiner
	GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) -o bin/combiner-status-linux-arm ./cmd/combiner-status

# x86_64 Linux (lab servers / future non-Pi hosts)
build-linux-amd64:
	mkdir -p bin
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/combiner-linux-amd64 ./cmd/combiner
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/combiner-status-linux-amd64 ./cmd/combiner-status

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
		cp config/site*.example.yaml "$$stage/config/"; \
		cp -a config/allowlists "$$stage/config/"; \
		cp deploy/pi/install.sh \
			deploy/pi/prep-card.sh \
			deploy/pi/combiner-apply.sh \
			deploy/pi/generate-nftables.py \
			deploy/pi/generate-nftables.sh \
			deploy/pi/generate-network-config.py \
			deploy/pi/site_config.py \
			deploy/pi/README.md \
			"$$stage/deploy/pi/"; \
		cp -a deploy/pi/systemd deploy/pi/cloud-init "$$stage/deploy/pi/"; \
		chmod 755 "$$stage/deploy/pi/install.sh" \
			"$$stage/deploy/pi/prep-card.sh" \
			"$$stage/deploy/pi/combiner-apply.sh" \
			"$$stage/deploy/pi/cloud-init/combiner-firstboot.sh" \
			"$$stage/deploy/pi/generate-nftables.py" \
			"$$stage/deploy/pi/generate-nftables.sh" \
			"$$stage/deploy/pi/generate-network-config.py"; \
		tar -C "$(DIST)" -czf "$$stage.tar.gz" "$(PACKAGE_PREFIX)-linux-$$arch"; \
		echo "wrote $$stage.tar.gz"; \
	done
	@cd "$(DIST)" && (command -v sha256sum >/dev/null && sha256sum $(PACKAGE_PREFIX)-linux-*.tar.gz || shasum -a 256 $(PACKAGE_PREFIX)-linux-*.tar.gz) > SHA256SUMS
	@echo "ok: packages in $(DIST)/ (VERSION=$(VERSION))"

# The installer and card-prep scripts only run for real on a Pi, so lint is the
# main automated guard they get. Skip loudly rather than silently passing.
shellcheck:
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck -S warning deploy/pi/install.sh deploy/pi/prep-card.sh \
			deploy/pi/combiner-apply.sh \
			deploy/pi/cloud-init/combiner-firstboot.sh deploy/pi/generate-nftables.sh; \
		echo "shellcheck OK"; \
	else \
		echo "shellcheck not installed — skipped"; \
	fi

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

# Render every shipped profile (config/site*.example.yaml), so a new profile
# is covered by CI and lands in release packages without editing this file.
generate-check:
	@for f in config/site*.example.yaml; do \
		profile="$$(basename "$$f" .yaml)"; \
		echo "generating $$profile"; \
		python3 deploy/pi/generate-nftables.py "$$f" /tmp/combiner-nftables-$$profile.conf || exit 1; \
		python3 deploy/pi/generate-network-config.py "$$f" /tmp/combiner-net-$$profile >/dev/null || exit 1; \
	done
	@$(MAKE) --no-print-directory nft-check
	@echo "ok: generated /tmp/combiner-nftables-*.conf and /tmp/combiner-net-*"

# `nft -c` parses against the live kernel, so it needs privileges even in check
# mode. Skip loudly when we cannot get them — never report a real nft failure
# as "not installed".
nft-check:
	@ls /tmp/combiner-nftables-*.conf >/dev/null 2>&1 || \
		{ echo "no generated rulesets in /tmp — run 'make generate-check' first" >&2; exit 1; }
	@if ! command -v nft >/dev/null 2>&1; then \
		echo "nft not installed — skipped nft -c"; \
	elif [ "$$(id -u)" = "0" ]; then \
		for f in /tmp/combiner-nftables-*.conf; do \
			nft -c -f "$$f" || { echo "nft -c FAILED: $$f" >&2; exit 1; }; \
		done; \
		echo "nft -c OK (root)"; \
	elif sudo -n true >/dev/null 2>&1; then \
		for f in /tmp/combiner-nftables-*.conf; do \
			sudo -n nft -c -f "$$f" || { echo "nft -c FAILED: $$f" >&2; exit 1; }; \
		done; \
		echo "nft -c OK (sudo)"; \
	else \
		echo "nft present but needs root — skipped nft -c (run: sudo make nft-check)"; \
	fi

check: fmt-check test test-py lint-py shellcheck generate-check build build-pi build-pi-arm build-linux-amd64
