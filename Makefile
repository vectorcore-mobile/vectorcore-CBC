.PHONY: build ui dev-ui test vet race fuzz verify clean

BINARY := cbc
VERSION ?= 0.1.0

# Pure Go, no CGO, no external C libraries.
export CGO_ENABLED := 0

build:
	mkdir -p bin
	go build -buildvcs=false -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/cbc

# Rebuild the embedded web UI (requires Node.js / npm). web/dist is
# committed to git, so this is only needed when editing UI source - go
# build/make build alone never require Node.
ui:
	cd web && npm install && npm run build

dev-ui: ## Vite dev server, proxying /v1, /healthz, /readyz, /metrics to :8087
	cd web && npm install && npm run dev

test:
	go test ./... -count=1

vet:
	go vet ./...

race:
	go test -race ./...

fuzz:
	GOMAXPROCS=2 go test ./internal/cap -run=^$$ -fuzz=FuzzParse -fuzztime=15s
	GOMAXPROCS=2 go test ./internal/cbs -run=^$$ -fuzz=FuzzEncode -fuzztime=15s
	GOMAXPROCS=2 go test ./internal/sbcap -run=^$$ -fuzz=FuzzHeader -fuzztime=15s

verify: test vet race build
	./bin/$(BINARY) -c config/cbc.yaml.example -check-config

clean:
	rm -rf bin
