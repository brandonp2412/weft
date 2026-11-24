GO ?= go
PREFIX ?= $(HOME)/.local
VERSION ?= 0.1.0
TAGS := goolm

.PHONY: all build run demo test check integration install clean
all: build
build:
	$(GO) build -tags $(TAGS) -trimpath -ldflags '-X main.version=$(VERSION)' -o bin/weft ./cmd/weft
run: build
	./bin/weft
demo: build
	./bin/weft --demo
test:
	$(GO) test -race -tags $(TAGS) ./...
check:
	$(GO) vet -tags $(TAGS) ./...
	@test -z "$$($(GO) fmt ./...)"
integration:
	./scripts/integration.sh
install: build
	install -Dm755 bin/weft $(DESTDIR)$(PREFIX)/bin/weft
clean:
	rm -rf bin
