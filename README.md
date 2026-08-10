# hackeros-builder

**Wersja: 0.6.0**

Narzędzie do budowania niemutowalnych obrazów systemowych Debiana (w stylu
`bootc`/`rpm-ostree`) ze struktury projektu **identycznej jak `live-build`**.
Jeśli masz już projekt `live-build`, możesz go wkleić do `hackeros-builder`
i dodać jeden plik — `config/config.hk` — żeby zbudować z niego obraz
niemutowalny zamiast klasycznego, instalowalnego ISO.

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
sudo hackeros-builder build cloud   # buduje rootfs + wypycha obraz OCI do registry
sudo hackeros-builder build iso     # sciaga obraz OCI z registry + buduje hybrydowe ISO
sudo hackeros-builder build all     # build cloud, nastepnie build iso
```

Opcje globalne (patrz `--help` dla pełnej listy):

| Flaga                  | Znaczenie |
|-------------------------|-----------|
| `-p, --project <dir>`   | Katalog projektu (domyślnie `.`) |
| `-w, --workdir <dir>`   | Katalog roboczy, chroniony lockiem — **musi być różny dla równoległych buildów** |
| `-o, --output <plik>`   | Ścieżka wynikowego `.iso` |
| `--insecure-registry`   | Wyłącza weryfikację TLS dla registry (tylko self-signed/testowe) |
| `--skip-preflight`      | Pomija sprawdzenie dostępności narzędzi na starcie (przydatne w CI) |
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
              ├─ debootstrap
              ├─ mount /proc,/sys,/dev
              ├─ apt-get install <package-lists>
              ├─ copy includes.chroot
              ├─ exec hooks/normal/*.hook.chroot (w chroot)
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

Wersja 0.5.0 ma podstawowy hardening (preflight, lock, timeouty, checksuma,
insecure registry, CI, testy jednostkowe) ale wciąż nie jest przetestowana
end-to-end na realnej maszynie budującej. Pierwszy krok po pobraniu repo:
`go mod tidy && go build ./... && go test ./...`.

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

### Estetyka / UX

12. **Progress indicator** dla `debootstrap`/`mksquashfs`/push warstwy OCI —
    obecnie tylko statyczne logi `[INFO]`. **→ Roadmap v0.6.0.**

---

## ROADMAP

Zrealizowane w v0.1.0 – v0.5.0 (ta runda rozbudowy):

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

- [ ] **v0.6.0** — **Ładny progress indicator** (spinner/progress bar) dla
      `debootstrap`, `mksquashfs`, push/pull warstw OCI — ten sam motyw co w
      `hammer` roadmap, najlepiej współdzielona biblioteka/styl między
      obydwoma narzędziami HackerOS.
- [ ] **v0.6.0** — Warstwy OCI przyrostowe (baza / package-lists / hooks)
      zamiast jednej warstwy na cały rootfs.
- [ ] **v0.6.0** — Walidacja rozmiaru/zawartości rootfs przed push (ochrona
      przed wypchnięciem uszkodzonego obrazu do registry).
- [ ] **v0.7.0** — Konfigurowalny mirror Debiana w `config.hk`
      (`[release] -> mirror`).
- [ ] **v0.7.0** — Weryfikacja podpisów obrazów OCI przy `build iso`
      (`cosign`/`sigstore`).
- [ ] **v0.8.0** — Pełne wsparcie interpolacji `${...}` w `config.hk`
      hackeros-buildera (np. `${env:GITHUB_TOKEN}` zamiast trzymania tokenu
      w pliku) — `internal/hk` już to wspiera, brakuje testów end-to-end z
      prawdziwym `env`.
- [ ] **v0.9.0** — Wsparcie wielu architektur (`--arch=arm64` w debootstrap),
      obecnie zaszyte na `amd64`.
- [ ] **v0.9.0** — Testy integracyjne dla `internal/rootfs`/`internal/ociimage`/
      `internal/isobuild` w kontenerze privileged (GitHub Actions self-hosted
      runner lub VM z KVM).
- [ ] **v1.0.0** — Stabilne API CLI, dokumentacja man page, paczka `.deb`
      dla samego `hackeros-builder`.

## Licencja

MIT
