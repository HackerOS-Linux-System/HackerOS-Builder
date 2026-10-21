# HackerOS Atomic – niezmienność i integracja z hammer

Analiza na podstawie źródeł hammera (`source-code/system/immutable.h#`,
`cli/cli_sys.h#`, `oci/`), `build.lua` (tryb `--atomic`) i `Scripts/`.

## Jak działa niezmienność w hammerze
- `hammer immutable enable` (backend `remount`) robi `mount -o remount,bind,ro`
  na: `/usr /bin /sbin /lib /lib64 /lib32 /libx32 /opt /srv /boot /hammer/store
  /hammer/profiles /snap`. Zawsze zapisywalne: `/etc /var /home /root /tmp /run
  /media /mnt /hammer/db /hammer/tmp` – **`/etc` jest zapisywalny z założenia**
  (tak jak w ostree), więc edycja np. `/etc/hostname` nanem jest dozwolona.
- Stan zapisują pliki `/hammer/db/.immutable-enabled` i `.immutable-backend`.
  Transakcje (`install/upgrade/oci deploy`) wołają `unlock_for_transaction()` →
  praca → `guard_release()` (relock).
- **Dlaczego to nie działało na zainstalowanym systemie:** `remount,bind` działa
  tylko na *punkcie montowania*. `/usr` na zwykłej partycji ext4 nim nie jest
  (hammer sam to odnotowuje w komentarzu: „`/usr` bez własnego montowania …
  zawsze UNLOCKED”). Dodatkowo `hammer immutable enable` jest pomijane w
  systemie live, a flaga nigdy nie była ustawiana przez Calamares.

## Co robi teraz builder (zgodnie z hammerem)
1. `hackeros-immutable.service` (sysinit.target) tworzy z katalogów z listy
   hammera osobne montowania bind (`mount --rbind d d`) i ustawia je `ro`
   (symlinki usrmerge i nieistniejące katalogi pomijane). `/usr/local` zostaje
   zapisywalny. Dzięki temu `hammer immutable status` pokazuje LOCKED, a
   `remount,bind` hammera ma na czym działać.
2. Gdy w obrazie jest `/usr/bin/hammer`, builder zapisuje
   `/hammer/db/.immutable-enabled` i `.immutable-backend` (= `remount`), więc
   hammer sam odblokowuje system na czas transakcji i blokuje go po niej.
3. Ręcznie: `hackeros-immutable status|lock|unlock|run -- <polecenie>`.
   Wyłączenie awaryjne: parametr jądra `hackeros.mutable=1` lub plik
   `/etc/hackeros/mutable`.
4. `apt`/`apt-get` są usunięte z obrazu; baza `dpkg` zostaje dla hammera.

Test: `sudo nano /usr/bin/ls` / `sudo touch /usr/x` → „Read-only file system”.
`/etc` pozostaje zapisywalny (patrz wyżej).

## Propozycje dla hammera
1. **Wspólne źródło prawdy** – `hammer immutable enable` powinien przed
   `remount,bind` sprawdzać `mountpoint -q` i robić `mount --rbind p p`, gdy
   ścieżka nie jest punktem montowania (wtedy `hackeros-immutable.service`
   przestaje być potrzebny). Dziś `do_remount` tylko loguje błąd.
2. **Usługa `hammer-immutable.service`** ma `ConditionVirtualization=no` – w
   QEMU/VM (testy) nie wystartuje, a `hackeros-immutable.service` tak. Warto
   zamienić na `!container`.
3. **`/etc` jako opcja** – jeśli HackerOS Atomic ma blokować także `/etc`,
   potrzebny jest model ostree (3-way merge) lub overlay z whitelistą
   (`passwd`, `shadow`, `hostname`, `NetworkManager/…`); dopisać do
   `rw_always` wyjątki zamiast całego `/etc`.
4. **Moduł instalatora `hammerdeploy`** – zamiast `unpackfs` (kopia 1:1 na
   ext4) shellprocess wywołujący `hammer oci deploy` na `/mnt/target` +
   `grub::update_grub`. Wtedy instalacja ma deploymenty i rollback.
5. **`hammer immutable status`** – dodać wiersz „service: hackeros-immutable”.

## Instalator – inne zmiany w tej iteracji
- `displaymanager` w sekwencji Calamares (po `users`) – tylko gdy w obrazie
  jest SDDM/LightDM/GDM; dzięki temu checkbox „Zaloguj automatycznie” działa.
- `Scripts/remove-live-user.sh` jest osadzony w builderze; w chroocie używana
  jest wersja z `/usr/share/HackerOS/Scripts/`, jeśli istnieje (usuwa konto
  „user” i autologin live po instalacji).
- Bez restartu po „Ukończ”: komunikat na tty1 i wyłączenie po 10 s z odliczaniem.
