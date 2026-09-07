# Common dev tasks. CI runs the same gates from .github/workflows/ci.yml.

GO        ?= go
# .exe on Windows, where a file without one cannot be executed at all.
EXE       := $(if $(filter Windows_NT,$(OS)),.exe,)
BIN       ?= ./google-sheets-mcp$(EXE)
VERSION   ?= dev
PKG        = github.com/mmedum/google-sheets-mcp
LDFLAGS    = -s -w -X $(PKG)/internal/version.Version=$(VERSION)
COVER_MIN ?= 80
# The gates are one binary. Building it once and running it saves six
# links per `make check`, which is several seconds every time.
GATES     ?= ./.gates

# The tools, pinned to the versions CI uses and fetched the way CI
# fetches them.
#
# Not whatever is on the PATH. A distribution's golangci-lint built with
# an older Go refuses this module outright — and says so as "can't load
# config", which names the wrong thing — and a stale go-licenses fails on
# the standard library. Both passed for months in CI while `make check`
# was broken locally, which is the wrong way round: this file's first
# line promises the two are the same.
GOLANGCI_LINT ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
GOVULNCHECK   ?= golang.org/x/vuln/cmd/govulncheck@v1.7.0
GOLICENSES    ?= github.com/google/go-licenses@v1.6.0
# The module path is zricethezav, not gitleaks: the project moved
# organisation and the module path did not follow it. The version has to
# match the one the CI action bundles, which the pin gate checks.
GITLEAKS      ?= github.com/zricethezav/gitleaks/v8@v8.30.1

.PHONY: all
all: check

.PHONY: build
build: ## Build the binary
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/google-sheets-mcp

.PHONY: install
install: ## go install the binary
	CGO_ENABLED=0 $(GO) install -trimpath -ldflags="$(LDFLAGS)" ./cmd/google-sheets-mcp

.PHONY: fmt
fmt: ## Fail if gofmt would change anything
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt issues:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## go vet, including the tagged tests so they keep compiling
	$(GO) vet ./...
	$(GO) vet -tags=live ./...

.PHONY: lint
lint:
	$(GO) run $(GOLANGCI_LINT) run

.PHONY: test
test: ## Unit tests with the race detector and coverage
	$(GO) test -race -coverpkg=./internal/...,./cmd/... -coverprofile=cov.out -covermode=atomic ./...

.PHONY: gates
gates: ## Build the repository's own checks
	@$(GO) build -o $(GATES) ./scripts/gates

.PHONY: cover
cover: test gates ## Enforce the coverage floor per package
	@$(GATES) coverage cov.out $(COVER_MIN)

.PHONY: bench
bench: ## Benchmarks (phase 3 fills these in)
	$(GO) test -run XXX -bench . -benchmem ./internal/...

.PHONY: tidy
tidy: ## go.mod and go.sum are what `go mod tidy` would write
	$(GO) mod tidy -diff

.PHONY: vuln
vuln:
	$(GO) run $(GOVULNCHECK) ./...

.PHONY: licenses
licenses:
	$(GO) run $(GOLICENSES) check ./... --allowed_licenses=Apache-2.0,BSD-2-Clause,BSD-3-Clause,MIT,ISC

.PHONY: classes
classes: gates ## The error vocabulary, held closed from both sides
	@$(GATES) classes

.PHONY: secrets
secrets: ## Credentials, as CI scans for them
	$(GO) run $(GITLEAKS) dir . --config .gitleaks.toml --no-banner

.PHONY: leaks
leaks: gates ## Identifiers and data in the working tree
	@$(GATES) leaks

.PHONY: leaks-history
leaks-history: gates ## Every blob and message in the history; run before going public
	@$(GATES) leaks history

.PHONY: transcript
transcript: gates ## The live drivers print only through their redactor
	@$(GATES) transcript

.PHONY: live-cover
live-cover: build gates ## The live driver must exercise every tool option
	@$(GATES) live-cover $(BIN)

.PHONY: mcpb
mcpb: gates ## The Claude Desktop bundle's manifest, against the files it will pack
	@$(GATES) mcpb

.PHONY: parity
parity: gates ## `make check` and ci.yml run the same things
	@$(GATES) parity

.PHONY: pins
pins: gates ## Actions pinned by SHA, tool versions exact, shells pinned
	@$(GATES) pins

.PHONY: schemas
schemas: build ## Dump the tool schemas
	$(BIN) --dump-schemas > schemas.json

.PHONY: schema-diff
schema-diff: build gates ## Diff the tool schemas against the last tag
	@$(GATES) schema-diff $(BIN)

.PHONY: smoke
smoke: build gates ## Drive the binary over stdio
	@$(GATES) smoke $(BIN)

.PHONY: staleness
staleness: build gates ## The docs must match the code
	@$(GATES) staleness $(BIN)

.PHONY: hooks
hooks: ## Point git at the repository's own hooks
	git config core.hooksPath .githooks

.PHONY: live
live: build ## Drive the built binary against a real account (see docs/development.md)
	$(GO) run -tags=live ./scripts/livesheet -bin $(BIN)

.PHONY: evals
evals: build ## Drive a model through the tools and score it (needs credentials and the claude CLI)
	$(GO) run -tags=live ./scripts/evals -bin $(BIN)

.PHONY: check
check: fmt vet tidy lint cover vuln licenses secrets classes leaks transcript live-cover mcpb parity pins schema-diff smoke staleness ## Everything CI runs

.PHONY: clean
clean:
	$(RM) $(BIN) $(BIN).exe $(GATES) $(GATES).exe cov.out schemas.json
