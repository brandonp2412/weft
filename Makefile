GO ?= go
GOFMT ?= gofmt
PREFIX ?= $(HOME)/.local
VERSION ?= 0.1.0
TAGS := goolm
DIST_NAME = weft-$(VERSION)-$(shell $(GO) env GOOS)-$(shell $(GO) env GOARCH)
EXE = $(shell $(GO) env GOEXE)

.PHONY: all build run demo screenshots test check integration install dist clean
all: build
build:
	$(GO) build -tags $(TAGS) -trimpath -ldflags '-X main.version=$(VERSION)' -o bin/weft ./cmd/weft
run: build
	./bin/weft
demo: build
	./bin/weft --demo
screenshots: build
	./scripts/screenshots.sh
test:
	$(GO) test -race -tags $(TAGS) ./...
check:
	$(GO) vet -tags $(TAGS) ./...
	@unformatted="$$($(GOFMT) -l cmd internal)" && \
		{ test -z "$$unformatted" || { printf '%s\n' "$$unformatted"; exit 1; }; }
integration:
	./scripts/integration.sh
install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 bin/weft "$(DESTDIR)$(PREFIX)/bin/weft$(EXE)"
dist: build
	install -d "bin/$(DIST_NAME)"
	install -m 0755 bin/weft "bin/$(DIST_NAME)/weft$(EXE)"
	install -m 0644 LICENSE "bin/$(DIST_NAME)/LICENSE"
	GO="$(GO)" TAGS="$(TAGS)" bash scripts/third-party-notices.sh "bin/$(DIST_NAME)/weft$(EXE)" > "bin/$(DIST_NAME)/THIRD-PARTY-NOTICES.txt"
	tar -C bin -czf "bin/$(DIST_NAME).tar.gz" "$(DIST_NAME)"
clean:
	rm -rf bin
