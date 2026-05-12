BINARY  = hackeros-builder
CMD     = ./cmd/hackeros-builder
VERSION = 0.1.0
LDFLAG  = -ldflags "-X main.Version=$(VERSION) -s -w"

.PHONY: all build install clean

all: build

build:
	@echo "  ▸ Kompilowanie $(BINARY) v$(VERSION)..."
	go build $(LDFLAG) -o $(BINARY) $(CMD)
	@echo "  ✔ Gotowe: ./$(BINARY)"

install: build
	@echo "  ▸ Instalowanie do /usr/local/bin/..."
	install -m 755 $(BINARY) /usr/local/bin/$(BINARY)
	@echo "  ✔ Zainstalowano!"

clean:
	rm -f $(BINARY)
