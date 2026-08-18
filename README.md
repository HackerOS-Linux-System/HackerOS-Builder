# hackeros-builder

**Wersja: 0.5.0**

Narzędzie do budowania niemutowalnych obrazów systemowych Debiana (w stylu
`bootc`/`rpm-ostree`) ze struktury projektu **identycznej jak `live-build`**.
Jeśli masz już projekt `live-build`, możesz go wkleić do `hackeros-builder`
i dodać jeden plik — `config/config.hk` — żeby zbudować z niego obraz
niemutowalny zamiast klasycznego, instalowalnego ISO.

## Co nowego w v0.5.0

**`[project] -> type = containerized` przeprojektowany: Isolator zamiast Distrobox.**
Poprzednia implementacja (v0.4.0) generowała manifest Distrobox jako własne
przybliżenie nieznanego wtedy `helpers/containerized`. Po dostarczeniu
źródeł [Isolatora](https://github.com/HackerOS-Linux-System/Isolator)
(podman-owy menedżer pakietów HackerOS + "Isolator Builder", jego własne
narzędzie budujące minimalne obrazy bazowe z wbudowanym Isolatorem) --
`type = containerized` został przepisany, żeby podążać za tym samym,
realnym pomysłem zamiast za zgadywanką:

- Buduje kontener roboczy dokładnie jak `type = container` (ten sam rootfs,
  ten sam brak hammer/atomowości), plus dopisuje `podman` i
  `ca-certificates` do pakietów projektu (Isolator jest podman-owym
  menedżerem pakietów -- bez podmana `isolator install` nie ma szans
  zadziałać; ta sama para pakietów co w prawdziwym Isolator Builderze).
- Pobiera najnowszą wersję Isolatora z GitHub Releases
  (`https://github.com/HackerOS-Linux-System/Isolator/releases/download/<wersja>/isolator.tar.gz`,
  wykrywaną automatycznie przez scraping strony `/releases` -- ta sama
  technika co dla `hammer`, celowo bez GitHub API żeby uniknąć rate-limitu)
  i wypakowuje **każdy zwykły plik** z archiwum bezpośrednio do
  `/usr/bin/` wewnątrz rootfs, z `chmod a+x` na każdym -- dokładnie jak
  opisano: rozpakuj do `/usr/bin/`, nadaj prawa wykonywania. Można
  nadpisać konkretną wersję przez `[project] -> isolator_version`.
  Weryfikacja SHA256 całego archiwum (best-effort, jeśli wydanie publikuje
  `checksums.txt` -- ten sam mechanizm co dla `hammer`).
- Dopisuje systemd unit `isolator-first-boot.service` uruchamiający
  `isolator init` przy pierwszym starcie -- skopiowany 1:1 z prawdziwego
  Isolator Buildera (`builder/pipeline.go`, `writeFirstBootUnit`), plus
  symlink `/usr/local/bin/isolator -> /usr/bin/isolator` (ta jednostka w
  oryginale odwołuje się do tej ścieżki).
- **Uwzględnia jawnie, że Isolator może w przyszłości zostać przepisany w
  innym języku** -- `internal/download/isolator.go` nie zakłada niczego o
  Go: traktuje wydanie wyłącznie jako "archiwum z gotowymi binarkami do
  rozpakowania", więc zadziała identycznie niezależnie od implementacji,
  dopóki logika wydawania (GitHub Releases, `isolator.tar.gz`, gotowe
  binarki w archiwum) zostanie zachowana.

Efekt końcowy: `hackeros-builder build container` na projekcie z
`type = containerized` produkuje kontener który po `podman run`/`docker run`
ma od razu działające `isolator init` i `isolator install <pakiet>` --
bez ręcznej instalacji Isolatora w środku.

## Co nowego w v0.4.0

**Progress bar dla push/pull OCI z REALNYMI bajtami.** `remote.WithProgress`
(API go-containerregistry) wpiete w push -- pasek pokazuje faktyczne bajty
wyslane do registry (`v1.Update.Complete`). Pull rowniez ma realny pasek:
`layer.Compressed()` + nowy `util.CountingReader` licza bajty faktycznie
odczytane wzgledem `layer.Size()` (znany z manifestu PRZED pobraniem, wiec
total jest dokladny, nie przyblizony).

**Warstwy OCI przyrostowe zamiast jednej warstwy na caly rootfs.**
`internal/rootfs/layers.go` -- `Builder.Build()` robi migawki stanu rootfs w
4 punktach kontrolnych i pakuje TYLKO roznice miedzy nimi jako osobne
warstwy OCI: `base` (debootstrap+MAC+cybersecurity), `packages` (pakiety
projektu), `hooks` (includes.chroot + hooks/normal + hooks/live), `runtime`
(hammer + `/etc/hammer/oci.hk`). Wykrywanie zmian oparte o (rozmiar, tryb,
mtime) kazdej sciezki -- bez haszowania tresci kazdego pliku (co
zaprzeczaloby celowi przyrostowosci). Usuniete pliki dostaja whiteout OCI
(`.wh.<nazwa>`), ktory `internal/ociimage` juz odczytywal od wersji 0.7.0.
Korzysc: przy przebudowie gdzie zmienily sie TYLKO hooki, warstwy
`base`/`packages` sa bajt-w-bajt identyczne jak poprzednio -- zgodne z OCI
registry (content-addressed storage) NIE musi ich ponownie przechowywac ani
wysylac.

**Konfigurowalny mirror Debiana i architektura.** `[release] -> mirror`
(domyslnie `http://deb.debian.org/debian`) i `[release] -> arch` (domyslnie
`amd64`) w `config.hk` -- oba przekazywane wprost do `debootstrap`.
**UWAGA UCZCIWOSCI**: `arch` niezawodnie wplywa TYLKO na rootfs
(debootstrap) -- `build iso` wypisuje jawne ostrzezenie gdy `arch` rozni
sie od architektury hosta budujacego, bo `grub-mkrescue` obecnie NIE
dostaje jawnego `-d <platforma>` i korzysta z modulow GRUB zainstalowanych
NA HOSCIE -- pelne wsparcie cross-arch ISO (przekazanie `-d` + walidacja
pakietow `grub-efi-<arch>-bin`) to osobna pozycja w ROADMAP.

**Podpisywanie i weryfikacja obrazow OCI (cosign, key-based).** Nowy pakiet
`internal/cosign` opakowuje binarke `cosign` (sigstore). `[project] -> sign
= true` podpisuje obraz PO udanym `build cloud` (kluczem prywatnym z
`cosign_key`). `[project] -> verify_signature = true` weryfikuje podpis
PRZED pociagnieciem obrazu w `build iso` (kluczem publicznym z
`cosign_key`) -- build jest PRZERYWANY jesli podpis jest
nieprawidlowy/brakujacy. Celowo key-based, NIE keyless/OIDC -- keyless
wymaga logowania przez przegladarke, co nie nadaje sie do w pelni
nienadzorowanego builda z linii komend.

**`cybersecurityPackages()` konfigurowalna z `config.hk`.** Nowy klucz
`[project] -> cybersecurity_packages` (tablica stringow) NADPISUJE
wbudowana liste pakietow cybersecurity/pentest gdy niepusty -- dziala
oczywiscie tylko dla `[project] -> type = cybersecurity`. Puste/brak klucza
= zachowanie identyczne jak w v0.7.0/v0.8.0 (wbudowana lista domyslna).

**Walidacja rootfs przed push do registry.** Nowa funkcja
`rootfs.ValidateRootfs` sprawdza (PRZED `build cloud`/`build container`
wypchnie cokolwiek): minimalny rozmiar calkowity (lapie przerwany w polowie
debootstrap/instalacje), obecnosc kluczowych sciezek (`etc`, `usr/bin`,
`var/lib/dpkg`), minimalna liczbe pakietow w bazie dpkg, oraz (dla pelnego
atomowego builda) obecnosc `/etc/hammer/oci.hk`. Blad walidacji przerywa
build PRZED zmarnowaniem czasu/transferu na wypchniecie niekompletnego
obrazu.

**Realna implementacja `[project] -> type = containerized`** (zamiast
placeholdera z v0.8.0). Buduje kontener roboczy DOKLADNIE jak
`type = container`, ale DODATKOWO generuje manifest Distrobox
(`distrobox.ini`, format `distrobox assemble`) -- `distrobox assemble
create --file <manifest>` + `distrobox enter <nazwa>`, czyli to samo
doswiadczenie co `hacker enter <kontener>` znane z ekosystemu HackerOS
(Hacker-CLI-Tool). **UWAGA UCZCIWOSCI**: repozytorium
`helpers/containerized` w glownym repo HackerOS jest niedostepne do
automatycznego wglądu (robots.txt) -- ta implementacja NIE jest
zweryfikowana 1:1 kopia tamtego skryptu, tylko wlasna, dzialajaca
implementacja hackeros-buildera oparta o publicznie udokumentowany wzorzec
kontenerow HackerOS (Distrobox).
> **⚠ ZASTĄPIONE w v0.10.0** -- po dostarczeniu prawdziwych źródeł
> Isolatora, `type = containerized` został przepisany żeby używać
> Isolatora zamiast tego przybliżenia Distroboxem. Patrz sekcja "Co nowego
> w v0.10.0" wyżej.

## Co nowego w v0.8.0

**Prawdziwy, kolorowy progress bar.** `internal/util/progress.go` --
pasek postepu ANSI aktualizowany w miejscu, pokazujacy REALNY postep
(current/total policzone z faktycznie wykonanej pracy), nie animacje "na
oko":
- `debootstrap` -- licznik tyka na faktycznych liniach
  `Retrieving/Validating/Extracting/...` z wyjscia debootstrap.
- Instalacja pakietow (`apt-get install`) -- total wyliczany PRZED
  instalacja przez `apt-get install -y -s` (dry-run, apt NIC nie zmienia,
  tylko pokazuje plan), postep zliczany na liniach `Setting up ...`
  faktycznej instalacji.
- Wykonywanie hookow -- pasek na liczbe hookow, z etykieta jezyka kazdego
  hooka.

Bezpieczny dla CI/pipe/redirect -- gdy stdout nie jest terminalem, pasek
NIE rysuje sie w trakcie (nie zasmieca logow), tylko jedno zwiezle
podsumowanie na koniec.

**Duzo ladniejszy CLI.** Nowa paleta (`internal/util/logging.go`):
pogrubienia, `Section()` (naglowek etapu w ramce), `Step()`/`SubStep()`
(ponumerowane, kolorowe kroki zamiast surowego `[INFO] Krok N/M`),
`PrintErrorBox()` -- kazdy blad konczacy build wyswietla sie teraz w
wyraznej czerwonej ramce zamiast ginac w dziesiatkach linii logow.

**Hooki w dowolnym jezyku -- nie tylko w shellu.** Kazdy hook (`config/
hooks/{normal,live,installer}/*.hook.chroot`) juz zawsze mogl deklarowac
dowolny interpreter przez shebang (`#!/usr/bin/env python3`) -- to
mechanizm jadra Linux, nie ograniczenie hackeros-buildera. Czego
brakowalo: (1) automatycznej instalacji interpretera PRZED probą
wykonania (bez tego pierwszy hook w Pythonie konczyl sie kryptycznym
bledem, bo `python3` nie byl jeszcze zainstalowany), (2) czytelnego
logowania w jakim jezyku dany hook jest napisany, (3) jasnego bledu z
lista wspieranych jezykow gdy shebang wskazuje na cos nierozpoznanego.
Od v0.8.0 wszystko to dziala automatycznie (`internal/hooklang`) dla:
`sh`/`bash` (wbudowane), `python3` (oraz `python` -> `python-is-python3`),
`ruby`, `lua5.1`/`lua5.3`/`lua5.4`, `perl`, `nodejs`, `php-cli`, `tcl`,
`r-base-core`, `gawk`, `fish`/`zsh`/`ksh`.

**Nowy katalog `config/hooks/installer/`.** Osobna kategoria hookow
(`liveparse.Project.InstallerHooks`), wykonywana NIE podczas budowy rootfs,
tylko podczas `build iso`, PO wstrzyknieciu i skonfigurowaniu instalatora
Calamares -- sluza do customizacji SAMEGO INSTALATORA (wlasny
`branding.desc`, dodatkowe moduly Calamares, wlasny wallpaper/logo),
NIGDY nie trafiaja do systemu docelowego. Dziala gdy instalator jest
wlaczony (`[project] -> installer != none`, czyli zarowno dla
`installer=default` jak i `installer=cybersecurity`) -- ten sam
mechanizm auto-instalacji interpreterow co dla `hooks/normal`/`hooks/live`.

**Bugfix: instalator NIE odpalal sie ponownie po pierwszym reboocie
(teraz naprawione).** `internal/isobuild` buduje JEDEN squashfs uzywany
zarowno jako live-medium (z ktorego startuje instalator) JAK I jako
zrodlo kopiowane 1:1 na dysk uzytkownika przez modul `unpackfs`
Calamares. Do wersji 0.7.0 `shellprocess.conf` (krok wykonywany w chroot
do systemu DOCELOWEGO, tuz przed `umount`) robil WYLACZNIE
`mkdir -p /etc/hammer` -- nie usuwal autologinu roota na tty1 ani
autostartu Calamares, ktore sa wypalone w tym samym squashfs. Skutek:
**pierwszy reboot po zakonczonej instalacji odpalalby instalator
PONOWNIE**, zamiast normalnie zalogowac do świeżo zainstalowanego
systemu. Naprawione: `shellprocess.conf` usuwa teraz
`/etc/systemd/system/getty@tty1.service.d/autologin.conf`,
wygenerowany `/root/.bash_profile`, `/usr/local/sbin/hackeros-installer-xinit`,
`/etc/hackeros-installer/` i `/etc/calamares/` z systemu docelowego, w
chroot, PRZED `umount`. Przy okazji usunieta zostala tez flaga `-d`
(debug) z wywolania `calamares` -- produkcyjny instalator nie powinien
domyslnie pokazywac panelu debugowania.

## Co nowego w v0.7.0

**`vendor/` jest teraz dostarczany w repo, wypełniony i przetestowany.**
Do tej pory `go.sum`/`vendor/` w ogóle nie były częścią repozytorium —
`make build` (domyślny cel, `-mod=vendor`) nie mógł zadziałać "z pudełka",
a `make vendor-sync`/`go mod vendor` wymagały dostępu do
`proxy.golang.org` i `gopkg.in`, które bywają zablokowane w niektórych
sieciach/firewallach. Od tej wersji:

- `go.sum` i `vendor/` są gotowe w repo — `make build` **działa od razu,
  offline, bez żadnego połączenia z siecią** (zweryfikowane z
  `GOPROXY=off`).
- Moduły `golang.org/x/sys`, `golang.org/x/sync`, `gopkg.in/yaml.v2`,
  `gopkg.in/yaml.v3`, `gopkg.in/check.v1`, `gotest.tools/v3` mają wpisy
  `replace` na końcu `go.mod`, wskazujące na dokładnie te same wersje
  kodu opublikowane równolegle na `github.com` — to wpływa **wyłącznie**
  na `make tidy`/`make vendor-sync` (regenerację `vendor/` po zmianie
  zależności), nie na zwykły `make build`, który i tak korzysta z już
  gotowego `vendor/`.

## Co nowego w v0.7.0 (tryby pracy buildera)

**`cybersecurity` przestało być cichym aliasem `default`.** Do wersji
0.6.0 zarówno `[project] -> type = cybersecurity`, jak i
`[project] -> installer = cybersecurity` były parsowane poprawnie, ale
**nie miały żadnego efektu** — w praktyce zachowywały się identycznie jak
`default`. Od v0.7.0 obie wartości są realne:

- **`[project] -> type = cybersecurity`** — dokłada do rootfs dodatkowy
  zestaw pakietów cybersecurity/pentest z domyślnych repozytoriów Debiana
  (`nmap`, `wireshark`, `tcpdump`, `hydra`, `john`, `hashcat`,
  `aircrack-ng`, `sqlmap`, `nikto`, `radare2`, `binwalk`, `foremost`,
  `steghide`, `gdb`, ...) — patrz `internal/rootfs`, `cybersecurityPackages`.
  Odpowiednik edycji "Cybersecurity Edition" (Red Team) z głównego
  repozytorium HackerOS, w zakresie możliwym do zrealizowania samym `apt`.
- **`[project] -> installer = cybersecurity`** — Calamares dostaje inny
  branding ("HackerOS Cybersecurity Edition", czerwony akcent zamiast
  niebieskiego) i kilka dodatkowych narzędzi diagnostycznych
  (`nmap`, `net-tools`, `tcpdump`, `dnsutils`) już w samym środowisku
  live/instalatora — patrz `internal/isobuild/installer.go`.

Te dwa pola są od siebie niezależne — można mieć `type=default` z
`installer=cybersecurity` (albo odwrotnie).

**Nowy tryb pracy buildera: `container`.** `[project] -> type = container`
plus nowa komenda `hackeros-builder build container` budują **zwykły
kontener roboczy** (OCI, kompatybilny z `podman`/`docker`) do codziennej
pracy — **nie** obraz atomowy/bootc:

- Rootfs budowany identycznie jak dla `default` (debootstrap +
  package-lists + hooks + includes.chroot), ale **bez** wstrzykiwania
  `hammer`/`/etc/hammer/oci.hk` (kontener roboczy nie jest zarządzany
  atomowo) i **bez** usuwania `apt`/`apt-get`.
- Wynik to lokalne archiwum `.tar` zapisywane w
  `<workdir>/container/<nazwa>-<tag>.tar`, wczytywalne od razu przez
  `podman load -i <plik>` albo `docker load -i <plik>` — **bez** potrzeby
  konta w żadnym registry.
- Jeśli `[account]`/`[auth]` w `config.hk` są wypełnione i nie podano
  `--local-only`, ten sam obraz jest **dodatkowo** wypychany do registry
  (wygodne do współdzielenia kontenera z innymi maszynami).
- Obraz dostaje sensowną domyślną konfigurację uruchomieniową
  (`Cmd=/bin/bash`, `WorkingDir=/root`) — `podman run -it --rm
  <repo>:<tag>` działa od razu, bez błędu "no command specified".
- `build iso`/`build all` na projekcie z `type=container` kończą się
  jasnym błędem tłumaczącym że kontener roboczy nie produkuje ISO —
  zamiast cichej, mylącej próby budowy instalowalnego systemu.

**Placeholder: `[project] -> type = containerized`.** Zarezerwowane pod
przyszłą integrację z narzędziem
[`containerized`](https://github.com/HackerOS-Linux-System/HackerOS/tree/main/helpers/containerized)
z głównego repozytorium HackerOS. Wartość jest już rozpoznawana przez
parser configu (nie jest to błąd "nieznana wartość"), ale każda komenda
`build` kończy się dla niej **jasnym, wczesnym błędem** (przed
kosztownym `debootstrap`) odsyłającym do powyższego repozytorium —
świadomie, zamiast cichego fallbacku do `build cloud` albo no-opa.
> **⚠ ZASTĄPIONE** — od v0.9.0 to już nie placeholder (manifest
> Distrobox), a od v0.10.0 realna integracja z Isolatorem. Patrz sekcje
> "Co nowego" nowszych wersji wyżej.

## Co nowego w v0.6.0

**Migracja `deb-ostree` → `hammer`.** `hackeros-builder` jest teraz
**całkowicie niezależny od `deb-ostree`** — jedynym narzędziem
zarządzania pakietami/atomowością wstrzykiwanym do budowanego obrazu jest
`hammer`, w trybie OCI (`oci-mode.tar.gz`). Zmiany:

- `internal/download/hammer.go` (zastępuje `debostree.go`) — ściąga
  `oci-mode.tar.gz` z `https://github.com/HackerOS-Linux-System/hammer/releases`,
  rozpakowuje z niego pojedynczą binarkę `hammer`, weryfikuje SHA256 całego
  archiwum jeśli wydanie publikuje `checksums.txt`.
- `internal/hkgen/hammer_config.go` (zastępuje `debostree_config.go`) —
  generuje `/etc/hammer/oci.hk` zamiast `/etc/deb-ostree/deb-ostree.hk`.
- `internal/rootfs/hammer_deps.go` (zastępuje `debostree_deps.go`) —
  instaluje biblioteki dynamiczne realnie wymagane przez `hammer`
  (`libostree-1-1`, `libglib2.0-0t64`, `liblzma5`, `libbz2-1.0`,
  `libgcc-s1`), zweryfikowane bezpośrednio z `readelf -d` na wydaniu
  `hammer` v0.6.0 — lista krótsza niż dla `deb-ostree`, bo `hammer` (Rust)
  statycznie linkuje HTTP/TLS/GPG.
- **`apt`/`apt-get` usuwane z finalnie zainstalowanego systemu.** W kroku
  `build iso`, po wstrzyknięciu instalatora Calamares a przed spakowaniem
  `filesystem.squashfs`, `hackeros-builder` usuwa `apt`, `apt-get` i
  pomocnicze narzędzia (`apt-cache`, `apt-config`, `apt-cdrom`, `apt-mark`,
  `apt-key`, `/usr/lib/apt`). **Baza `dpkg` (`/var/lib/dpkg/*`) i sam
  `dpkg` zostają nietknięte** — `hammer` czyta tę bazę bezpośrednio.

## Co nowego w v0.1.0 – v0.5.0

W tej rundzie rozbudowy `hackeros-builder` przeszedł z "działającego
szkieletu" do narzędzia z podstawowym hardeningiem produkcyjnym:

- **`internal/preflight`** — sprawdzenie dostępności `debootstrap`,
  `mksquashfs`, `grub-mkrescue`, `xorriso`, `mount`/`umount`/`chroot` w
  `$PATH` **na samym starcie**, z jednym komunikatem listującym wszystko
  czego brakuje (plus `apt install` z konkretnymi pakietami). Bez tego błąd
  wypływał dopiero w połowie wieloetapowego builda.
- **`internal/buildlock`** — lockfile (`flock(2)`) na `workDir`, chroniący
  przed dwoma równoległymi buildami nadpisującymi sobie te same pliki
  tymczasowe. Druga próba `build` na tym samym `workDir` dostaje czytelny
  błąd natychmiast, zamiast cichej korupcji danych.
- **`internal/httpclient`** — wspólny klient HTTP z jawnym `Timeout`
  (30s dla krótkich zapytań, dłuższy budżet na bezczynność połączenia dla
  transferów registry) zamiast `http.DefaultClient`, który mógł zawiesić
  cały build w nieskończoność przy martwym połączeniu.
- **Weryfikacja SHA256** pobranego archiwum `oci-mode.tar.gz` (hammer) —
  `download.DownloadHammer` sprawdza sumę kontrolną z `checksums.txt`
  opublikowanego przy wydaniu (jeśli wydanie go publikuje; w przeciwnym
  razie wypisuje wyraźne ostrzeżenie i kontynuuje, nie blokując starszych
  tagów).
- **`--insecure-registry`** — opcjonalne wyłączenie weryfikacji TLS dla
  self-signed/wewnętrznych registry testowych, podłączone przez
  `remote.WithTransport` w `go-containerregistry`. Nigdy włączone domyślnie.
- **`.github/workflows/ci.yml`** — pipeline `go build`/`go vet`/`go test`/`gofmt`
  na każdy push/PR.
- **Testy jednostkowe** dla `internal/hk` (parser .hk), `internal/preflight`,
  `internal/buildlock`, `internal/download`, `internal/config`.

## Dlaczego to istnieje

`live-build` jest świetny do budowania klasycznych obrazów Debiana, ale nic
w tym łańcuchu nie tworzy obrazu **OCI** jako jednostki dystrybucji systemu —
a to jest fundament modelu immutable/bootc (Fedora/RHEL ma to rozwiązane,
Debian nie miał). `hackeros-builder` wypełnia tę dziurę: interpretuje
strukturę `live-build` samodzielnie (bez wywoływania `lb build`), buduje
rootfs, pakuje go jako obraz OCI, wypycha do registry, i z tego obrazu może
zbudować bootowalne ISO z `hammer` już wstrzykniętym do `/usr/bin/` i
usuniętym `apt`/`apt-get` na finalnie zainstalowanym dysku (baza `dpkg`
pozostaje — czyta ją bezpośrednio `hammer`).

## Komendy

```bash
sudo hackeros-builder build cloud       # buduje rootfs + wypycha obraz OCI do registry
sudo hackeros-builder build iso         # sciaga obraz OCI z registry + buduje hybrydowe ISO
sudo hackeros-builder build all         # build cloud, nastepnie build iso
sudo hackeros-builder build container   # buduje zwykly kontener roboczy (podman/docker)
```

Opcje globalne (patrz `--help` dla pełnej listy):

| Flaga                  | Znaczenie |
|-------------------------|-----------|
| `-p, --project <dir>`   | Katalog projektu (domyślnie `.`) |
| `-w, --workdir <dir>`   | Katalog roboczy, chroniony lockiem — **musi być różny dla równoległych buildów** |
| `-o, --output <plik>`   | Ścieżka wynikowego `.iso` |
| `--insecure-registry`   | Wyłącza weryfikację TLS dla registry (tylko self-signed/testowe) |
| `--skip-preflight`      | Pomija sprawdzenie dostępności narzędzi na starcie (przydatne w CI) |
| `--local-only`          | (tylko `build container`) Nie wypychaj do registry, zapisz WYŁĄCZNIE lokalne archiwum `.tar` |
| `-v, --verbose`         | Logi DEBUG |

- **`build cloud`** — preflight (`debootstrap`/`chroot`/`mount`) → lock na
  `workDir` → buduje rootfs (debootstrap + hooks + package-lists), wstrzykuje
  `hammer` (z weryfikacją SHA256) i wygenerowany config `/etc/hammer/oci.hk`,
  pakuje całość jako jednowarstwowy obraz OCI i wypycha go do registry.
  `apt`/`apt-get` **pozostają** w tym obrazie na tym etapie — są jeszcze
  potrzebne w kroku `build iso` do wstrzyknięcia instalatora Calamares.
- **`build iso`** — preflight (`mksquashfs`/`grub-mkrescue`/`xorriso`) →
  ściąga obraz OCI z registry (dokładnie ten, który istnieje tam *teraz*),
  aktualizuje w nim `[origin]` w `/etc/hammer/oci.hk`, wstrzykuje instalator
  Calamares (apt-get), **usuwa `apt`/`apt-get`** (baza `dpkg` zostaje — czyta
  ją `hammer`), i dopiero z tak przygotowanego rootfs buduje klasyczny
  hybrydowy ISO (BIOS+UEFI). Od tego momentu system, który użytkownik
  zainstaluje na dysku przez Calamares, nie ma już `apt`/`apt-get`.
- **`build all`** — preflight dla obu etapów na starcie (zanim zacznie się
  kosztowny `debootstrap`) → jeden lock na cały przepływ → `build cloud`,
  a następnie `build iso` na obrazie który właśnie został wypchnięty.
- **`build container`** — preflight (`debootstrap`/`chroot`/`mount`, ten
  sam zestaw co `build cloud`) → lock na `workDir` → buduje rootfs
  (debootstrap + hooks + package-lists), **bez** wstrzykiwania
  `hammer`/`/etc/hammer/oci.hk` i **bez** usuwania `apt`/`apt-get` →
  pakuje go jako zwykły obraz OCI/Docker i zapisuje lokalne archiwum
  `<workdir>/container/<nazwa>-<tag>.tar` (wczytywalne przez `podman
  load`/`docker load`, bez potrzeby registry) → jeśli
  `[account]`/`[auth]` są wypełnione i `--local-only` nie było podane,
  dodatkowo wypycha ten sam obraz do registry. Przeznaczone dla
  `[project] -> type = container` — do budowania *zwykłego kontenera
  roboczego* na codzień, nie systemu instalowalnego na dysk.

## Struktura projektu

Identyczna jak `live-build`, plus jeden dodatkowy plik:

```
moj-projekt/
├── config/
│   ├── config.hk                 ← WYMAGANE: jedyny plik specyficzny dla hackeros-builder
│   ├── package-lists/
│   │   └── moje-pakiety.list.chroot
│   ├── hooks/
│   │   └── normal/
│   │       └── 0100-cos.hook.chroot
│   ├── includes.chroot/
│   │   └── etc/moj-plik.conf
│   └── archives/
│       ├── moje-repo.list.chroot
│       └── moje-repo.key.chroot
```

Możesz wkleić istniejący katalog `config/` z projektu `live-build` 1:1 —
`hackeros-builder` interpretuje te same podkatalogi (`package-lists/`,
`hooks/normal/`, `includes.chroot/`, `archives/`) tą samą logiką co
`live-build` (debootstrap + wykonanie hooków w chroot + kopiowanie plików),
tylko **bez wywoływania** `lb build` — cała interpretacja jest reimplementowana
natywnie w Go (`internal/liveparse`, `internal/rootfs`).

### config/config.hk

Jedyny plik, którego `live-build` nie ma. Format to `.hk`
(specyfikacja: hackeros-linux-system.github.io/HackerOS-Website/tools-docs/hk.html):

```
[account]
-> type => user            ! "user" albo "organisation"
-> name => michal

[auth]
-> token => ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

[release]
-> name => trixie           ! bookworm / trixie / forky / sid / unstable
```

| Sekcja      | Klucz  | Znaczenie |
|-------------|--------|-----------|
| `[account]` | `type` | `user` → obraz trafia na konto użytkownika w registry; `organisation` → na konto organizacji |
| `[account]` | `name` | nazwa użytkownika/organizacji w registry (np. GitHub) |
| `[auth]`    | `token`| token autoryzacyjny do `push` obrazu OCI (np. GitHub PAT z `write:packages` dla `ghcr.io`) |
| `[release]` | `name` | wersja Debiana przekazywana do `debootstrap` jako `SUITE` |
| `[release]` | `mirror` | mirror Debiana dla `debootstrap` (opcjonalne, domyślnie `deb.debian.org`) |
| `[release]` | `arch` | architektura docelowa dla `debootstrap` (opcjonalne, domyślnie `amd64`) — patrz zastrzeżenie o `build iso`/GRUB w opisie v0.9.0 wyżej |
| `[project]` | `type` | tryb pracy buildera: `default` / `cybersecurity` / `normal` / `official` / `independent` / `container` / `containerized` — patrz opis pełny w `config/config.hk` |
| `[project]` | `installer` | wariant instalatora ISO: `default` / `cybersecurity` / `none` |
| `[project]` | `cybersecurity_packages` | nadpisuje wbudowaną listę pakietów cybersecurity (tablica, tylko dla `type=cybersecurity`) |
| `[project]` | `sign` / `verify_signature` / `cosign_key` | podpisywanie/weryfikacja obrazu OCI przez `cosign` (key-based) |

## Co hackeros-builder robi automatycznie

1. **Wstrzykuje `hammer`** — podczas budowy rootfs ściąga najnowszą wersję
   z `https://github.com/HackerOS-Linux-System/hammer/releases` (archiwum
   `oci-mode.tar.gz`, lub wersję wskazaną w zmiennej środowiskowej
   `HAMMER_VERSION`), rozpakowuje z niego pojedynczą binarkę `hammer` i
   umieszcza w `rootfs/usr/bin/hammer` z uprawnieniami `a+x`. Instaluje też
   jego biblioteki dynamiczne (`libostree-1-1`, `libglib2.0-0t64`,
   `liblzma5`, `libbz2-1.0`, `libgcc-s1`).
2. **Generuje `/etc/hammer/oci.hk`** — gotowy plik konfiguracyjny dla
   `hammer`, żeby system zbudowany przez `hackeros-builder` od razu po
   pierwszym boocie miał poprawny `[origin] -> refspec` (`docker://...`)
   wskazujący na obraz OCI, z którego powstał.
3. **Usuwa `apt`/`apt-get` z finalnie zainstalowanego systemu** — w kroku
   `build iso`, po wstrzyknięciu Calamares a przed spakowaniem
   `filesystem.squashfs` (czyli dokładnie w treści, którą Calamares kopiuje
   1:1 na dysk użytkownika), `hackeros-builder` usuwa binarki `apt`,
   `apt-get`, `apt-cache`, `apt-config`, `apt-cdrom`, `apt-mark`, `apt-key`
   oraz katalog `/usr/lib/apt`. **Baza `dpkg` (`/var/lib/dpkg/*`) oraz sam
   `dpkg` pozostają nietknięte** — `hammer` czyta tę bazę bezpośrednio do
   odczytu listy zainstalowanych pakietów, więc jej usunięcie zepsułoby
   `hammer`. Obraz OCI wypychany przez samo `build cloud` (bez `build iso`)
   **nadal zawiera** `apt`/`apt-get` — patrz punkt 3 w sekcji „Co trzeba
   dopracować”.

## Wymagania

```bash
sudo apt install debootstrap squashfs-tools grub-pc-bin grub-efi-amd64-bin xorriso mtools
```

Go 1.22+ do budowania samego `hackeros-builder` (binarka końcowa nie
wymaga Go w runtime).

## Budowanie

> **Uwaga o `go.mod`:** przy okazji tej rundy zmian poprawiono literówkę w
> deklaracji modułu — było `module .../HackerOS-Builder` (wielka litera),
> a WSZYSTKIE importy wewnętrzne w calym kodzie od poczatku uzywaly
> `.../hackeros-builder` (mala litera) -- przez co `go build` nie
> uruchamial sie w ogole (Go nie rozwiazywalby importow wzgledem
> zadeklarowanej sciezki modulu). Poprawiono na `hackeros-builder`
> (male litery), zgodnie z rzeczywistymi importami. **Cały projekt,
> łącznie z tą zmianą i migracją deb-ostree → hammer, został faktycznie
> skompilowany (`go build ./...`), sprawdzony (`go vet ./...`) i
> przetestowany (`go test ./...`) lokalnie** (z tymczasowymi `replace` na
> `golang.org/x/sys`/`golang.org/x/sync` -> mirrory na `github.com`, bo
> środowisko w którym to pisano miało dostęp tylko do `github.com`, nie do
> `golang.org`; w normalnym środowisku z pełnym dostępem do internetu te
> `replace` nie są potrzebne i nie są częścią tego repo) — wszystko
> przechodzi bez błędów.

> **Uwaga o `go.sum`:** ten plik (lockfile z hashami zależności) nie jest
> commitowany w repozytorium — środowisko, w którym ten kod został
> napisany, nie miało dostępu do internetu, więc nie dało się obliczyć
> prawdziwych hashy modułów (a commitowanie fałszywych/pustych hashy
> psowałoby build identycznie jak ich brak). To **nie są zewnętrzne
> ścieżki czy literówki w imporcie** — `internal/ociimage` poprawnie
> importuje pakiety z `go-containerregistry`, którego wersja jest podana
> w `go.mod`; brakuje tylko samego lockfile. Wygeneruj go jednym poleceniem
> przed pierwszym budowaniem:

```bash
go mod tidy
# lub: make setup
```

To ściągnie `go-containerregistry` i policzy jego hashe do `go.sum`.
Po tym `go build ./...` zadziała normalnie.

```bash
go build -o hackeros-builder .
sudo ./hackeros-builder build all -p ./moj-projekt -o ./moj-system.iso
```

---

## Architektura

```
config/config.hk ──────┐
                        ▼
              internal/config (parsuje .hk)
                        │
config/{package-lists,  ▼
 hooks,includes.chroot} internal/liveparse (interpretuje strukture live-build)
                        │
                        ▼
              internal/rootfs.Builder
              ├─ debootstrap                         (pasek postepu)
              ├─ mount /proc,/sys,/dev
              ├─ apt-get install <package-lists>      (pasek postepu)
              ├─ copy includes.chroot
              ├─ ensureHookInterpreters (python3/ruby/lua/...)
              ├─ exec hooks/normal|live/*.hook.chroot (w chroot, dowolny jezyk)
              ├─ download.DownloadHammer -> /usr/bin/hammer
              └─ hkgen.WriteHammerConfig -> /etc/hammer/oci.hk
                        │
         ┌──────────────┴───────────────┐
         ▼                              ▼
  internal/ociimage.BuildAndPush   (build cloud)
  (tar.gz warstwa -> v1.Image ->
   remote.Write do registry)
         │
         ▼
   Registry OCI (np. ghcr.io)
         │
         ▼ (build iso sciaga TEN SAM obraz z powrotem)
  internal/ociimage.PullAndUnpack
  (remote.Image -> warstwy -> rootfs
   z obsluga whiteoutow OCI)
         │
         ▼
  internal/isobuild.Build
  ├─ InjectInstaller: apt-get install calamares + xorg (w rootfs z registry)
  ├─ exec hooks/installer/*.hook.chroot (customizacja Calamares, dowolny jezyk)
  ├─ rootfs.RemoveAptTooling -> usuwa apt/apt-get (baza dpkg zostaje)
  ├─ mksquashfs rootfs -> filesystem.squashfs   (JUZ bez apt/apt-get)
  ├─ copy vmlinuz + initrd.img
  ├─ generuj grub.cfg
  └─ grub-mkrescue -> hybrydowe ISO (BIOS+UEFI)
```

### Format .hk

Parser pełnej specyfikacji `.hk` (sekcje, zagnieżdżenie `->`/`-->`/`--->`,
klucze kropkowe, interpolacja `${...}` i `${env:...}`, tablice, typy
string/number/bool) żyje w `internal/hk`. To jest implementacja referencyjna
dla całego ekosystemu HackerOS — `hammer` (Rust) ma swój **podzbiór** tego
parsera wystarczający dla jego własnego configu (`/etc/hammer/oci.hk`);
jeśli ten config w przyszłości potrzebuje interpolacji czy głębszego
zagnieżdżenia, logika z `internal/hk` (Go) jest wzorcem do portu na Rust.

> **Uwaga o strukturze `/etc/hammer/oci.hk`:** klucze pól (`refspec`,
> `osname`, `repo_path`, `lists_paths`, `sources_list`, `sources_dir`,
> `keyring_dir`, `require_gpg`) zostały zweryfikowane wprost z binarki
> wydania `hammer` v0.6.0 (`strings`/`readelf`), ponieważ `hammer` nie
> publikuje osobnej dokumentacji schematu configu w chwili pisania tego
> kodu. Grupowanie tych kluczy w sekcje w `internal/hkgen/hammer_config.go`
> jest odwzorowaniem analogicznym do poprzedniego `deb-ostree.hk` — jeśli
> faktyczny parser `hammer` oczekuje innego układu sekcji, dostosuj
> `hkgen.GenerateHammerConfig`; same klucze są zweryfikowane.

`internal/hkgen` używa `internal/hk` do programowego wygenerowania
`/etc/hammer/oci.hk` (fluent `Builder`/`SectionBuilder` API) bez ręcznego
sklejania stringów.

---

## Co trzeba dopracować, żeby to było narzędzie produkcyjne

Wersja 0.7.0 ma podstawowy hardening (preflight, lock, timeouty, checksuma,
insecure registry, CI, testy jednostkowe) ale wciąż nie jest przetestowana
end-to-end na realnej maszynie budującej. Pierwszy krok po pobraniu repo
(w pełni offline, `vendor/` jest już w repo):
`make build && make test`.
Odpowiednik bez Makefile: `go build -mod=vendor ./... && go test -mod=vendor ./...`.

### Krytyczne przed pierwszym użyciem produkcyjnym

1. **Realna kompilacja i testy end-to-end na czystej maszynie Debian**
   Kod przeszedł przegląd logiki i testy jednostkowe (`internal/hk`,
   `internal/preflight`, `internal/buildlock`, `internal/download`,
   `internal/config`), ale nie był jeszcze uruchomiony jako pełny
   `build cloud`/`build iso`/`build all` na żywej maszynie z `debootstrap`.
   Sprawdź szczególnie `internal/ociimage` (zależność od
   `go-containerregistry` — wersje API submodułów mogły się zmienić między
   wydaniami biblioteki od czasu napisania tego kodu).

2. **Autoryzacja registry inna niż Basic+token**
   `ociimage.BuildAndPush`/`PullAndUnpack` używają `authn.Basic` z dowolną
   nazwą użytkownika i tokenem jako hasłem — to działa dla `ghcr.io`, ale
   inne registry (Docker Hub, prywatne Harbor) mogą wymagać innego flow
   (np. `authn.Bearer`, OAuth2 token exchange).

3. **Brak walidacji rozmiaru/zawartości rootfs przed push**
   Nie ma sprawdzenia czy rootfs nie jest pusty/uszkodzony przed
   zapakowaniem do warstwy OCI — błąd w `debootstrap` mógłby skutkować
   pchnięciem zepsutego obrazu do registry.

4. **`checksums.txt` musi zostać opublikowany przez wydania `hammer`**
   Weryfikacja SHA256 w `download.DownloadHammer` jest *gotowa po stronie
   `hackeros-builder`*, ale działa tylko jeśli wydania `hammer` na
   GitHub Releases publikują plik `checksums.txt` w formacie `sha256sum`
   (`<hex>  oci-mode.tar.gz`). Bez tego pliku weryfikacja jest pomijana z
   ostrzeżeniem — to nie jest błąd `hackeros-builder`, ale wymaga
   skoordynowanej zmiany w pipeline release `hammer`.

5. **`apt`/`apt-get` nadal obecne w obrazie OCI wypychanym przez samo
   `build cloud`** (bez `build iso`)
   Usunięcie `apt`/`apt-get` następuje w `internal/isobuild`, PO
   wstrzyknięciu Calamares, PONIEWAŻ Calamares wciąż go potrzebuje żeby
   zainstalować siebie i swoje zależności (Xorg, openbox, itd.) w rootfs
   pociągniętym z registry — apt/apt-get NIE są usuwane już na etapie
   `build cloud`, bo to złamałoby ten krok. Jeśli obraz OCI ma być
   wdrażany bezpośrednio (`hammer oci deploy ...`) bez przechodzenia przez
   ISO/Calamares, apt/apt-get pozostaną obecne w takim wdrożeniu —
   docelowo powinno to być finalizowane po stronie samego `hammer`
   (analogicznie do tego, jak obrazy `bootc`/`rpm-ostree` nigdy nie mają
   menedżera pakietów hosta wewnątrz zbudowanego drzewa).

### Ważne, ale nie blokujące pierwszego wydania

6. **Jedna warstwa OCI dla całego rootfs**
   `createLayerTarball` pakuje cały rootfs jako jedną warstwę — proste, ale
   nieefektywne dla `upgrade` (cały obraz trzeba ściągnąć ponownie nawet przy
   drobnej zmianie). Warstwy przyrostowe (baza / package-lists / hooks)
   zmniejszyłyby transfer przy aktualizacjach przez `hammer`.

7. **Brak weryfikacji podpisów / `--policy` przy pull obrazu OCI**
   Tak jak w `hammer`, `PullAndUnpack` nie weryfikuje podpisów obrazu
   (`cosign`/`sigstore`).

8. **Konfigurowalny mirror Debiana**
   `defaultMirror` jest zaszyty na sztywno (`deb.debian.org`) — warto
   dodać opcjonalny klucz w `config.hk` (np. `[release] -> mirror`).

9. **EFI boot image dla `grub-mkrescue`**
   Obecna implementacja zakłada, że `grub-mkrescue` ma dostęp do
   `/usr/lib/grub/x86_64-efi` (pakiet `grub-efi-amd64-bin`) — sprawdzić na
   docelowym systemie budującym, czy obraz UEFI faktycznie się generuje.

10. **`buildlock` jest specyficzny dla Linuksa (`syscall.Flock`)**
   Nie jest to problem dla `hackeros-builder` (który i tak wymaga
   `debootstrap`/`chroot`/`mount`, czyli działa tylko na Linuksie), ale
   warto to udokumentować jawnie — próba `go build` na innym systemie
   operacyjnym (np. do samych testów `internal/hk` na macOS) nie skompiluje
   całego modułu z powodu `internal/buildlock`.

11. **Brak testów dla `internal/rootfs`, `internal/ociimage`, `internal/isobuild`**
    Te pakiety wymagają roota i rzeczywistych narzędzi systemowych
    (`debootstrap`, `mksquashfs`) lub żywego registry OCI do przetestowania
    — nie są łatwe do pokrycia testami jednostkowymi w CI bez kontenera
    privileged. `internal/hk`, `internal/preflight`, `internal/buildlock`,
    `internal/download`, `internal/config` mają testy; reszta wymaga
    środowiska integracyjnego (patrz pkt 1).

12. **`build container` nie był jeszcze uruchomiony end-to-end**
    Kod przeszedł przegląd logiki, `go build`/`go vet`/`go test` (w tym
    nowe testy `internal/config`), ale `hackeros-builder build container`
    nie był jeszcze uruchomiony jako pełny build na żywej maszynie z
    `debootstrap` + faktyczny `podman load`/`docker load` wynikowego
    archiwum. Sprawdź szczególnie że `mutate.Config` w
    `internal/ociimage/local.go` daje obraz który `podman run` akceptuje
    bez dodatkowych flag.

13. **`[project] -> type = containerized` (Isolator) nie był jeszcze uruchomiony end-to-end**
    Kod przeszedł przegląd logiki i testy jednostkowe
    (`internal/download/isolator_test.go`), ale nie był jeszcze
    uruchomiony przeciwko prawdziwemu, opublikowanemu wydaniu Isolatora
    (`isolator.tar.gz` pobrany na żywo, faktyczny `podman run` na
    zbudowanym kontenerze + `isolator init` + `isolator install <pakiet>`
    w środku). Sprawdź szczególnie: (a) czy prawdziwe archiwum wydania ma
    strukturę zgodną z założeniem "spłaszcz do /usr/bin/" w
    `extractAllToDir`, (b) czy `isolator-first-boot.service` faktycznie
    się odpala w kontenerze uruchamianym przez `podman run` (containery
    bez `--systemd=always` mogą nie mieć PID 1 = systemd w ogóle — to
    zależy od tego jak docelowo uruchamiacie te kontenery).

### Estetyka / UX

14. **Progress indicator** dla `debootstrap`/`mksquashfs`/push warstwy OCI —
    obecnie tylko statyczne logi `[INFO]`. **→ Roadmap v0.8.0.**

---

## ROADMAP

Zrealizowane w v0.10.0 (ta runda rozbudowy):

- [x] `[project] -> type = containerized` przepisany na Isolator
      (https://github.com/HackerOS-Linux-System/Isolator) zamiast
      przybliżenia Distroboxem z v0.9.0 -- pobiera najnowsze wydanie z
      GitHub Releases, wypakowuje do `/usr/bin/`, `chmod a+x`, dopisuje
      `podman`/`ca-certificates` do pakietów, systemd unit
      `isolator-first-boot.service` (1:1 z prawdziwym Isolator Builderem)
      -- `internal/download/isolator.go`, `internal/buildflow/container.go`
- [x] `[project] -> isolator_version` -- nadpisanie automatycznie
      wykrywanej najnowszej wersji Isolatora
- [x] Testy jednostkowe dla `internal/download/isolator.go`

Zrealizowane w v0.9.0:

- [x] Progress bar dla push/pull OCI z realnymi bajtami (`remote.WithProgress`,
      `util.CountingReader` na `layer.Compressed()`)
- [x] Warstwy OCI przyrostowe (base/packages/hooks/runtime) zamiast jednej
      warstwy na caly rootfs -- `internal/rootfs/layers.go`,
      `ociimage.BuildAndPushLayers`
- [x] Konfigurowalny mirror Debiana i architektura (`[release] -> mirror`,
      `[release] -> arch`) -- z jawnym ostrzezeniem o ograniczeniach
      cross-arch dla `build iso` (grub-mkrescue)
- [x] Podpisywanie i weryfikacja obrazow OCI przez cosign, key-based
      (`internal/cosign`, `[project] -> sign`/`verify_signature`/`cosign_key`)
- [x] `cybersecurityPackages()` konfigurowalna z `config.hk`
      (`[project] -> cybersecurity_packages`)
- [x] Walidacja rozmiaru/zawartosci rootfs przed push do registry
      (`rootfs.ValidateRootfs`)
- [x] Testy jednostkowe dla `internal/rootfs/layers.go`,
      `internal/rootfs/validate.go`, nowych kluczy `config.hk`

Zrealizowane w v0.8.0:

- [x] Prawdziwy, kolorowy progress bar z realnym postepem
      (`internal/util/progress.go`) -- wpiety w debootstrap, instalacje
      pakietow, wykonywanie hookow
- [x] Znaczaco ladniejszy CLI -- `Section()`/`Step()`/`SubStep()`/
      `PrintErrorBox()` w `internal/util/logging.go`
- [x] Hooki w dowolnym jezyku (python3/ruby/lua/perl/nodejs/php/tcl/R/
      gawk/fish/zsh/ksh, nie tylko sh/bash) z automatyczna instalacja
      interpretera -- nowy pakiet `internal/hooklang`
- [x] Nowy katalog `config/hooks/installer/` -- customizacja Calamares
      podczas `build iso`, dziala gdy instalator jest wlaczony
      (`liveparse.Project.InstallerHooks`, `internal/isobuild/installerhooks.go`)
- [x] **Bugfix**: instalator odpalal sie ponownie po pierwszym reboocie po
      instalacji (brak sprzatania autologin+autostart z systemu
      docelowego) -- naprawione w `calamaresShellprocessConf`
- [x] Usunieta flaga `-d` (debug) z wywolania `calamares` w produkcyjnym
      instalatorze
- [x] Testy jednostkowe dla `internal/hooklang` i nowej logiki hookow w
      `internal/liveparse`

Zrealizowane w v0.7.0:

- [x] `[project] -> type = cybersecurity` przestało być cichym aliasem
      `default` — dokłada realny zestaw pakietów cybersecurity/pentest
      do rootfs (`internal/rootfs`, `cybersecurityPackages`)
- [x] `[project] -> installer = cybersecurity` przestało być cichym
      aliasem `default` — realny branding "Cybersecurity Edition" +
      dodatkowe narzędzia diagnostyczne w live/instalatorze
      (`internal/isobuild/installer.go`)
- [x] Nowy tryb pracy buildera `[project] -> type = container` +
      komenda `hackeros-builder build container` — buduje zwykły
      kontener roboczy (podman/docker), bez hammer/atomowości
      (`internal/buildflow/container.go`, `internal/ociimage/local.go`)
- [x] Testy jednostkowe dla nowych wartości `[project] -> type`/`installer`
      (`internal/config/config_test.go`)

Zrealizowane w v0.1.0 – v0.5.0:

- [x] Sprawdzenie dostępności wymaganych narzędzi na starcie (`internal/preflight`)
- [x] Timeouty na żądaniach HTTP (`internal/httpclient`)
- [x] Weryfikacja checksumy SHA256 pobranego archiwum `hammer` (oci-mode.tar.gz)
- [x] Wsparcie registry self-signed/insecure (`--insecure-registry`)
- [x] Lockfile na `workDir` (`internal/buildlock`)
- [x] Minimalny pipeline GitHub Actions (`go build`/`go vet`/`go test`/`gofmt`)
- [x] Testy jednostkowe dla `internal/hk`, `internal/preflight`,
      `internal/buildlock`, `internal/download`, `internal/config`
- [x] `main.go` przeniesiony do korzenia repo (obok `go.mod`)

Pozostałe pozycje:

- [ ] **v1.0.0** — Pełne wsparcie cross-arch ISO: przekazanie jawnego
      `-d <platforma>` do `grub-mkrescue` + walidacja obecności pakietów
      `grub-efi-<arch>-bin` PRZED próbą budowy (obecnie tylko ostrzeżenie,
      patrz `warnIfForeignArch`).
- [ ] **v1.0.0** — Weryfikacja `[project] -> type = containerized` na
      żywym repo Isolatora (realne wydanie `isolator.tar.gz`, faktyczny
      `podman run` + `isolator init` + `isolator install` wewnątrz
      zbudowanego kontenera) — kod przeszedł przegląd logiki i testy
      jednostkowe (`internal/download/isolator_test.go`), ale nie był
      jeszcze uruchomiony end-to-end przeciwko prawdziwemu wydaniu
      Isolatora.
- [ ] **v1.0.0** — Pełne wsparcie interpolacji `${...}` w `config.hk`
      hackeros-buildera (np. `${env:GITHUB_TOKEN}` zamiast trzymania tokenu
      w pliku) — `internal/hk` już to wspiera, brakuje testów end-to-end z
      prawdziwym `env`.
- [ ] **v1.0.0** — Testy integracyjne dla `internal/rootfs`/`internal/ociimage`/
      `internal/isobuild` w kontenerze privileged (GitHub Actions self-hosted
      runner lub VM z KVM).
- [ ] **v1.0.0** — Stabilne API CLI, dokumentacja man page, paczka `.deb`
      dla samego `hackeros-builder`.

## Licencja

MIT
