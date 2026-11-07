.PHONY: build web test

build: web
	go build -o bin/dotshift ./cmd/dotshift

web:
	npm --prefix web ci --no-audit --no-fund
	npm --prefix web run build

test:
	npm --prefix web test
	go test ./...
