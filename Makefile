.PHONY: build-web build-hub build-agents build-all test demo vet clean

VERSION ?= 0.1.0
LDFLAGS := -s -w -X vpspulse/internal/version.Version=$(VERSION)

build-web:
	cd web && pnpm install --frozen-lockfile --config.strict-dep-builds=false && pnpm build

build-hub:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/vpspulse-hub ./cmd/hub

build-agents:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/vpspulse-agent-linux-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/vpspulse-agent-linux-arm64 ./cmd/agent

build-all: build-web build-agents build-hub

test:
	go test -v ./internal/...

vet:
	go vet ./...

demo: build-all
	./bin/vpspulse-hub --demo --listen :8080 --downloads-dir ./bin

clean:
	rm -rf bin web/dist/assets web/dist/index.html web/tsconfig.tsbuildinfo
