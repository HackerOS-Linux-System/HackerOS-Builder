# HackerOS Builder

Przyjazne narzędzie CLI do budowania obrazów ISO HackerOS.  
Działa jako nakładka na `live-build` lub całkowicie niezależnie (tryb standalone).

## Kompilacja

```bash
make build
# lub
go build -o hackeros-builder ./cmd/hackeros-builder
```

## Instalacja

```bash
sudo make install
```

## Szybki start

```bash
# Nowy projekt
hackeros-builder init moj-projekt
cd moj-projekt

# Edytuj konfigurację
nano config/config.hk

# Zainstaluj zależności (raz)
sudo hackeros-builder setup

# Zbuduj ISO
hackeros-builder build
```

## Komendy

| Komenda | Opis |
|---------|------|
| `init [katalog]` | Nowy projekt |
| `build [--release] [--standalone]` | Zbuduj ISO |
| `clean [--purge] [--standalone]` | Wyczyść |
| `setup [--release]` | Zainstaluj zależności |
| `migration` | Migruj z live-build |
| `info` | Informacje o projekcie |
| `lb <args>` | Przekaż do live-build |

## Tryby budowania

W `config/config.hk`:

```
-> mode => livebuild    # nakładka na live-build (domyślny)
-> mode => standalone   # całkowicie niezależny
```

Lub przez flagę CLI:
```bash
hackeros-builder build --standalone
```

## Format konfiguracji .hk

Dokumentacja: https://hackeros-linux-system.github.io/HackerOS-Website/tools-docs/hk.html

## Obsługiwane wersje Debian

- `bookworm` — Debian 12 (oldstable)
- `trixie` — Debian 13 (stable)
- `forky` — Debian 14 (testing)
