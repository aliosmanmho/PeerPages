# PeerPages — gelistirme komutlari.
#
# Windows'ta `make` kurulu olmayabilir; esdegerleri icin docs/DEV.md'ye bak
# (npm run ... ve scripts/*.ps1).

SHELL := /bin/sh
WEB   := web
AGENT := agent

# GitHub Pages alt yolu. Uretim derlemesi icin: make build PEERPAGES_BASE=/PeerPages/
PEERPAGES_BASE ?= /

.PHONY: help dev test test-web test-agent e2e build build-web build-agent lint lint-web lint-agent testdata clean

help: ## Komutlari listele
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

dev: ## Web'i localhost:5173'te ac (ajani ayri terminalde calistir)
	cd $(WEB) && npm run dev

test: test-web test-agent ## Tum birim testleri

test-web: ## Web birim testleri (vitest)
	cd $(WEB) && npm test

test-agent: testdata ## Ajan birim testleri (go test)
	cd $(AGENT) && go test ./...

e2e: ## Playwright e2e (Faz 1 kabul kriterleri)
	# Playwright kendisi derler: web'i dogru alt yolla, test verisini ve ajani.
	cd $(WEB) && npm run e2e

build: build-web build-agent ## Her ikisini derle

build-web: ## Web'i derle (onyukleyici + baglanti ekrani + sw.js)
	cd $(WEB) && node scripts/build.mjs $(PEERPAGES_BASE)

build-agent: ## Ajan binary'sini derle
	cd $(AGENT) && go build -o peerpages .

lint: lint-web lint-agent ## Tum lint/format kontrolleri

lint-web: ## biome + tsc
	cd $(WEB) && npm run lint

lint-agent: ## gofmt + go vet + golangci-lint (kurulu ise)
	cd $(AGENT) && gofmt -l . && go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && (cd $(AGENT) && golangci-lint run) || echo "golangci-lint kurulu degil, atlandi"

testdata: ## Test sitesi icin uretilen dosyalar (5 MB big.bin)
	@if [ -d $(AGENT)/internal/testsite/gen ]; then 		cd $(AGENT) && go run ./internal/testsite/gen; 	else 		echo "testsite ureticisi henuz yok (Faz 1.3), atlandi"; 	fi

clean: ## Uretilen dosyalari sil
	rm -rf $(WEB)/dist $(WEB)/test-results $(WEB)/playwright-report
	rm -f $(AGENT)/peerpages $(AGENT)/peerpages.exe $(AGENT)/testdata/site/big.bin
