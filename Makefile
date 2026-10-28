# PeerPages — development commands.
#
# `make` may not be installed on Windows; for the equivalents see
# docs/DEV.md (npm run ... and scripts/*.ps1).

SHELL := /bin/sh
WEB   := web
AGENT := agent

# The GitHub Pages subpath. For a production build: make build PEERPAGES_BASE=/PeerPages/
PEERPAGES_BASE ?= /

.PHONY: help dev test test-web test-agent e2e build build-web build-agent lint lint-web lint-agent testdata clean release-snapshot release-check

help: ## List the commands
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

dev: ## Open the web app on localhost:5173 (run the agent in another terminal)
	cd $(WEB) && npm run dev

test: test-web test-agent ## Every unit test

test-web: ## The web unit tests (vitest)
	cd $(WEB) && npm test

test-agent: testdata ## The agent unit tests (go test)
	cd $(AGENT) && go test ./...

e2e: ## Playwright e2e (the phase 1 acceptance criteria)
	# Playwright builds things itself: the web app with the right subpath, the test data and the agent.
	cd $(WEB) && npm run e2e

build: build-web build-agent ## Build both

build-web: ## Build the web app (the bootloader, the connect screen and sw.js)
	cd $(WEB) && node scripts/build.mjs $(PEERPAGES_BASE)

build-agent: ## Build the agent binary
	cd $(AGENT) && go build -o peerpages .

lint: lint-web lint-agent ## Every lint and format check

lint-web: ## biome + tsc
	cd $(WEB) && npm run lint

lint-agent: ## gofmt + go vet + golangci-lint (when installed)
	@# gofmt returns 0 even when it lists files: we turn an empty output into
	@# the expectation, because otherwise `make lint` passed silently on
	@# unformatted code.
	cd $(AGENT) && gofmt -l . | tee /dev/stderr | (! read)
	cd $(AGENT) && go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && (cd $(AGENT) && golangci-lint run) || echo "golangci-lint is not installed, skipped"

testdata: ## The generated files for the test site (the 5 MB big.bin)
	@if [ -d $(AGENT)/internal/testsite/gen ]; then 		cd $(AGENT) && go run ./internal/testsite/gen; 	else 		echo "the testsite generator does not exist yet (phase 1.3), skipped"; 	fi

clean: ## Remove the generated files
	rm -rf $(WEB)/dist $(WEB)/test-results $(WEB)/playwright-report dist
	rm -f $(AGENT)/peerpages $(AGENT)/peerpages.exe $(AGENT)/testdata/site/big.bin

release-snapshot: ## A local snapshot build with goreleaser (it does NOT publish)
	goreleaser release --snapshot --clean

release-check: ## Validate the goreleaser configuration
	goreleaser check
