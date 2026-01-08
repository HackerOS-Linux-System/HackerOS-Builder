# HackerOS Builder

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**HackerOS Builder** to potężne i szybkie narzędzie CLI (Command Line Interface) zaprojektowane do automatyzacji procesu budowania obrazów typu **Live Build**. Pozwala na błyskawiczne generowanie systemów operacyjnych dostosowanych do Twoich potrzeb.

---

## Komendy

Narzędzie jest proste w obsłudze dzięki intuicyjnym poleceniom:

| Komenda | Opis |
| :--- | :--- |
| `HackerOS-Builder build` | Rozpoczyna proces budowania obrazu. |
| `HackerOS-Builder profile` | Zarządza profilami konfiguracji obrazu. |

### Flagi budowania
Podczas używania komendy `build`, możesz skorzystać z następujących flag:
* `-stable` – Buduje obraz w oparciu o stabilne pakiety.
* `-here` – Wykonuje proces budowania w bieżącym katalogu roboczym.

---

## Instalacja

Wybierz preferowaną metodę instalacji:

### 1. Budowanie ze źródeł (Manualnie)
Jeśli chcesz mieć najnowszą wersję bezpośrednio z kodu:

```bash
# Sklonuj repozytorium
git clone [https://github.com/twoje-repo/HackerOS-Builder.git](https://github.com/twoje-repo/HackerOS-Builder.git)
cd HackerOS-Builder
```

# Uruchom proces budowania
hl run build.hacker

# Zainstaluj binarkę w systemie
cd source-code
sudo mv main HackerOS-Builder
sudo mv HackerOS-Builder /usr/bin/

### 2. Szybka instalacja (Menedżer Hacker)
Najprostszy sposób dla użytkowników środowiska Hacker:
 * Instalacja:
   hacker unpack hackeros-builder

 * Usuwanie (opcjonalne):
   hacker pack hackeros-builder
