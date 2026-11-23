.PHONY: build clean manifest package repository-tool test vet

GO ?= go
BINARY := tracearr-bridge-linux-amd64
DIST := dist

build:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags='-s -w' -o $(DIST)/$(BINARY) .

repository-tool:
	mkdir -p $(DIST)
	CGO_ENABLED=0 $(GO) build -trimpath -o $(DIST)/repository-tool ./cmd/repository-tool

manifest: build repository-tool
	./$(DIST)/$(BINARY) manifest | ./$(DIST)/repository-tool manifest > $(DIST)/manifest.json

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

package: test-race build repository-tool vet
	@test -n "$(TAG)" || (echo "Set TAG=vX.Y.Z" >&2; exit 1)
	python3 tools/package_release.py --tag "$(TAG)"

clean:
	rm -rf $(DIST)
