LDFLAGS := -X github.com/ashfaqc137/ownci/internal/buildinfo.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) \
	-X github.com/ashfaqc137/ownci/internal/buildinfo.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none) \
	-X github.com/ashfaqc137/ownci/internal/buildinfo.Date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: build build-dev test vet fmt fmtcheck check clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ownci ./cmd/ownci

build-dev:
	go build -trimpath -tags dev -ldflags "$(LDFLAGS)" -o bin/ownci-dev ./cmd/ownci

test:
	go test -race ./...
	go test -tags dev ./internal/buildinfo/

vet:
	go vet ./...
	go vet -tags dev ./...

fmt:
	gofmt -w .

fmtcheck:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

check: fmtcheck vet test build build-dev

clean:
	rm -rf bin/ dist/
