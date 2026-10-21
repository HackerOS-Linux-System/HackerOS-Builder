#!/bin/sh
# =====================================================================
# remove-live-user.sh - uruchamiany przez Calamares (modul shellprocess)
# W CHROOCIE ZAINSTALOWANEGO SYSTEMU, po module "users"/"displaymanager".
#
# Problem, ktory to naprawia: obraz live zawiera konto "user" (haslo "live",
# sudo bez hasla, autologin). Calamares kopiuje caly system live na dysk i
# DODAJE nowego uzytkownika, wiec po instalacji istnialy dwa konta:
# "user" oraz to utworzone w instalatorze.
#
# Skrypt jest idempotentny i nigdy nie zostawia systemu bez zadnego
# zwyklego uzytkownika: konto live jest usuwane tylko wtedy, gdy istnieje
# inne "zwykle" konto (UID >= 1000).
# =====================================================================
LIVE_USER="${HACKEROS_LIVE_USER:-user}"
LOG=/var/log/hackeros-remove-live-user.log

log() { echo "[remove-live-user] $*" | tee -a "$LOG" 2>/dev/null; }

# Inne zwykle konto niz live?
OTHER_USERS="$(awk -F: -v live="$LIVE_USER" '$3>=1000 && $3<65534 && $1!=live {print $1}' /etc/passwd)"

# 1. Sudo bez hasla dla konta live - usuwamy ZAWSZE (w zainstalowanym
#    systemie nie ma prawa istniec).
if [ -f /etc/sudoers.d/live-user ]; then
    rm -f /etc/sudoers.d/live-user && log "usunieto /etc/sudoers.d/live-user"
fi

# 2. Autologin konta live w display managerach.
for f in /usr/lib/sddm/sddm.conf.d/autologin.conf \
         /etc/sddm.conf.d/autologin.conf \
         /etc/sddm.conf.d/zz-blue-live-autologin.conf; do
    if [ -f "$f" ] && grep -Eq "^User=${LIVE_USER}\$" "$f"; then
        rm -f "$f" && log "usunieto autologin SDDM: $f"
    fi
done
if [ -f /etc/sddm.conf ] && grep -Eq "^User=${LIVE_USER}\$" /etc/sddm.conf; then
    sed -i '/^\[Autologin\]/,/^\[/{/^User=/d;/^Session=/d;/^Relogin=/d}' /etc/sddm.conf
    log "wyczyszczono [Autologin] w /etc/sddm.conf"
fi
if [ -f /etc/gdm3/daemon.conf ]; then
    sed -i "/^AutomaticLogin\(Enable\)\?=/d" /etc/gdm3/daemon.conf
fi
for f in /etc/lightdm/lightdm.conf.d/*autologin*.conf /etc/lightdm/lightdm.conf; do
    [ -f "$f" ] && sed -i "/^autologin-user=${LIVE_USER}\$/d" "$f"
done
# Jednostka live-only (jesli jest w obrazie)
systemctl disable blue-live-autologin.service >/dev/null 2>&1 || true
rm -f /usr/lib/systemd/system/blue-live-autologin.service \
      /usr/lib/hackeros/blue-live-autologin.sh

# 3. Znacznik trybu live Blue Environment nie moze trafic do nowego systemu
#    (inaczej po instalacji Blue znow pokazalby instalator na pelnym ekranie).
rm -f /etc/skel/.config/Blue-Environment/.live
for h in /home/*; do
    [ -d "$h" ] && rm -f "$h/.config/Blue-Environment/.live"
done

# 4. Samo konto live.
if ! id "$LIVE_USER" >/dev/null 2>&1; then
    log "konto '$LIVE_USER' nie istnieje - nic do usuniecia"
    exit 0
fi
if [ -z "$OTHER_USERS" ]; then
    log "UWAGA: brak innego zwyklego uzytkownika - NIE usuwam '$LIVE_USER' (i tak zablokuje haslo tylko jesli to nowe konto z instalatora)"
    exit 0
fi

pkill -u "$LIVE_USER" >/dev/null 2>&1 || true
if userdel -rf "$LIVE_USER" >>"$LOG" 2>&1; then
    log "usunieto konto live '$LIVE_USER' (pozostali: $(echo $OTHER_USERS))"
else
    # awaryjnie: jesli usuniecie sie nie powiodlo, przynajmniej zablokuj konto
    usermod -L -s /usr/sbin/nologin "$LIVE_USER" >>"$LOG" 2>&1 || true
    log "BLAD: userdel nie powiodl sie - konto '$LIVE_USER' zablokowane (patrz $LOG)"
fi
# grupa o tej samej nazwie moze zostac
getent group "$LIVE_USER" >/dev/null 2>&1 && groupdel "$LIVE_USER" >/dev/null 2>&1
rm -rf "/home/$LIVE_USER"
exit 0
