BINARY      := hackeros-builder
PREFIX      ?= /usr/local
INSTALL_DIR := $(DESTDIR)$(PREFIX)/bin

# Wersja: odczytywana z gita, fallback na "dev"
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# -mod=vendor: build uzywa katalogu vendor/ (jest w repo) zamiast
# sciagac moduly z internetu. Dziala w pelni offline. Jesli chcesz
# pominac vendor/, ustaw GOFLAGS="" lub uzyj "make build-mod".
BUILD_FLAGS := -mod=vendor -ldflags "-s -w -X main.version=$(VERSION)"

.DEFAULT_GOAL := build

.PHONY: build build-mod install uninstall clean test fmt vet tidy vendor-sync help

## build: kompiluje hackeros-builder (uzywa vendor/, offline)
build:
	@echo "[build] $(BINARY) v$(VERSION) (vendor, offline)..."
	go build $(BUILD_FLAGS) -o $(BINARY) .
	@echo "[build] ok: ./$(BINARY)"

## build-mod: kompiluje przez siec (go mod download, wymaga proxy.golang.org)
build-mod:
	@echo "[build-mod] $(BINARY) v$(VERSION) (siec, -mod=mod)..."
	GONOSUMDB='*' GOFLAGS=-mod=mod go build \
		-ldflags "-s -w -X main.version=$(VERSION)" \
		-o $(BINARY) .
	@echo "[build-mod] ok: ./$(BINARY)"

## install: kompiluje i instaluje do $(INSTALL_DIR)
install: build
	@echo "[install] $(INSTALL_DIR)/$(BINARY)"
	install -Dm755 $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "[install] ok"

## uninstall: usuwa $(INSTALL_DIR)/$(BINARY)
uninstall:
	@echo "[uninstall] usuwanie $(INSTALL_DIR)/$(BINARY)"
	rm -f $(INSTALL_DIR)/$(BINARY)

## clean: usuwa skompilowana binarka
##        (katalog roboczy buildu: "hackeros-builder clean [--all]")
clean:
	@echo "[clean] usuwanie ./$(BINARY)"
	rm -f $(BINARY)

## test: uruchamia testy jednostkowe (uzywa vendor/)
test:
	go test -mod=vendor ./...

## fmt: formatuje caly kod przez gofmt
fmt:
	gofmt -w .

## vet: uruchamia go vet
vet:
	go vet -mod=vendor ./...

## tidy: aktualizuje go.sum -- WYMAGA DOSTEPU DO INTERNETU (proxy.golang.org)
##       Uruchom po dodaniu nowych zaleznosci w go.mod, potem "make vendor-sync"
tidy:
	@echo "[tidy] go mod tidy (wymaga internetu)..."
	go mod tidy
	@echo "[tidy] ok -- zaktualizowano go.sum"

## vendor-sync: regeneruje vendor/ z aktualnego go.mod+go.sum
##              WYMAGA DOSTEPU DO INTERNETU przy pierwszym uruchomieniu.
##              Po wykonaniu: "make build" dziala w pelni offline.
vendor-sync:
	@echo "[vendor-sync] go mod vendor (wymaga internetu)..."
	GONOSUMDB='*' go mod vendor
	@echo "[vendor-sync] ok -- vendor/ zaktualizowany"

## help: wyswietla te pomoc
help:
	@echo "Dostepne cele:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
