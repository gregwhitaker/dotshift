.PHONY: brand brand-check build web test test-release menubar test-menubar test-menubar-lifecycle
.DEFAULT_GOAL := build

brand:
	node scripts/sync-brand.mjs

brand-check:
	node scripts/sync-brand.mjs --check

build: web
	go build -o dotshift ./cmd/dotshift

web: brand
	npm --prefix web ci --no-audit --no-fund
	npm --prefix web run build

test: web
	npm --prefix web test
	go test ./...
	$(MAKE) test-release

test-release:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/test-release.py

menubar: build
	bash scripts/build-menubar.sh

test-menubar:
	CLANG_MODULE_CACHE_PATH="$(CURDIR)/build/module-cache" SWIFTPM_MODULECACHE_OVERRIDE="$(CURDIR)/build/module-cache" swift test --disable-sandbox --cache-path build/swift-cache --package-path macos --scratch-path build/swift

test-menubar-lifecycle:
	python3 scripts/test-menubar-lifecycle.py
