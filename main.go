package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/buildflow"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

const version = "0.3.0"

// validSubcommands to jedyne akceptowane nazwy podkomendy (po opcjonalnym
// "build"). Uzywane do walidacji i do budowy czytelnego komunikatu bledu.
var validSubcommands = []string{"cloud", "iso", "all"}

func isValidSubcommand(s string) bool {
	for _, v := range validSubcommands {
		if s == v {
			return true
		}
	}
	return false
}

func printUsage() {
	b := util.Bold
	fmt.Printf(`%s %s -- budowanie niemutowalnych obrazow Debiana (OCI/bootc-style)

%s
  hackeros-builder [opcje] build <cloud|iso|all>
  hackeros-builder [opcje] <cloud|iso|all>          (forma skrocona, bez "build")
  hackeros-builder clean                            Usun katalog roboczy (--workdir).
  hackeros-builder clean --all                      Jak wyzej + usun wynikowy plik .iso (--output).

%s
  build cloud          Zbuduj rootfs (debootstrap + hooks + package-lists)
                        i wypchnij go jako obraz OCI do registry.
  build iso             Sciagnij obraz OCI z registry i zbuduj z niego
                        hybrydowy obraz ISO (bootowalny BIOS+UEFI), gotowy
                        do instalacji -- boot startuje PROSTO w graficzny
                        instalator (Calamares), bez posredniego pulpitu live.
  build all              Wykonaj 'build cloud', nastepnie 'build iso'.
  clean                  Usun katalog roboczy (rootfs/oci-push/iso-build/...).
  clean --all             Jak 'clean', plus usun wynikowy plik .iso.

%s
  -v, --verbose            Wlacz logi DEBUG.
  -p, --project <dir>      Katalog projektu (musi zawierac 'config/config.hk').
                           Domyslnie: katalog biezacy.
  -w, --workdir <dir>      Katalog roboczy na pliki tymczasowe.
                           Domyslnie: ./hackeros-build-work
                           UWAGA: rownolegle buildy MUSZA uzywac roznych
                           --workdir -- ten sam katalog jest chroniony
                           lockiem (flock) i drugi build poczeka/odmowi.
  -o, --output <plik>      Sciezka wynikowego pliku .iso (tylko dla 'build iso'
                           i 'build all'). Domyslnie: ./output.iso
  --insecure-registry      Wylacz weryfikacje TLS przy polaczeniu z registry
                           OCI. Uzyj TYLKO dla self-signed/wewnetrznych
                           registry testowych -- nigdy w produkcji.
  --skip-preflight         Pomin sprawdzenie dostepnosci wymaganych narzedzi
                           (debootstrap, mksquashfs, grub-mkrescue, ...) na
                           starcie. Przydatne w CI gdzie te sprawdzenie jest
                           juz zrobione osobno.
  --no-installer           (tylko 'build iso'/'build all') Nie wstrzykuj
                           graficznego instalatora (Calamares) do ISO --
                           wynikowy obraz to czyste live-medium bez kreatora
                           instalacji.
  --all                    (tylko 'clean') Usun rowniez plik wyjsciowy .iso,
                           nie tylko katalog roboczy.
  -h, --help               Wyswietl ta pomoc i wyjdz.
  -V, --version             Wyswietl wersje i wyjdz.

%s
  config/
    config.hk                      <- WYMAGANE: konto, token, wersja Debiana
    package-lists/*.list.chroot
    hooks/normal/*.hook.chroot
    includes.chroot/...
    archives/*.list.chroot
    archives/*.key.chroot

Wszystkie komendy 'build' wymagaja uprawnien roota (debootstrap, chroot, mount).
`,
		b("hackeros-builder"), version,
		b("Uzycie:"),
		b("Komendy:"),
		b("Opcje globalne:"),
		b("Struktura projektu (identyczna jak live-build, plus config/config.hk):"),
	)
}

func main() {
	args := os.Args[1:]

	var (
		verboseFlag      bool
		projectDir       = "."
		workDir          = "./hackeros-build-work"
		outputISO        = "./output.iso"
		insecureRegistry bool
		skipPreflight    bool
		noInstaller      bool
		cleanAll         bool
	)

	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-v", "--verbose":
			verboseFlag = true
		case "-p", "--project":
			i++
			if i >= len(args) {
				fail("opcja --project wymaga argumentu")
			}
			projectDir = args[i]
		case "-w", "--workdir":
			i++
			if i >= len(args) {
				fail("opcja --workdir wymaga argumentu")
			}
			workDir = args[i]
		case "-o", "--output":
			i++
			if i >= len(args) {
				fail("opcja --output wymaga argumentu")
			}
			outputISO = args[i]
		case "--insecure-registry":
			insecureRegistry = true
		case "--skip-preflight":
			skipPreflight = true
		case "--no-installer":
			noInstaller = true
		case "--all":
			cleanAll = true
		case "-h", "--help", "help":
			printUsage()
			os.Exit(0)
		case "-V", "--version":
			fmt.Println("hackeros-builder " + version)
			os.Exit(0)
		default:
			positional = append(positional, args[i])
		}
	}

	util.SetVerbose(verboseFlag)

	if insecureRegistry {
		util.Warnf("--insecure-registry jest wlaczone -- weryfikacja TLS dla " +
			"registry OCI jest WYLACZONA. Uzywaj tylko dla zaufanych, " +
			"wewnetrznych registry testowych.")
	}

	// "clean" / "clean --all" sa obslugiwane OSOBNO od "build <...>" --
	// nie wymagaja config/config.hk (projekt mogl juz zostac usuniety/
	// przeniesiony, a katalog roboczy nadal tam jest) i nie przechodza
	// przez logike preflight/buildflow. Wciaz wymagaja roota, bo pliki w
	// workDir (rootfs/, oci-push/, iso-build/) sa typowo wlasnoscia roota
	// (debootstrap/chroot je tworzyly).
	if len(positional) == 1 && positional[0] == "clean" {
		if os.Geteuid() != 0 {
			fail("hackeros-builder clean wymaga uprawnien roota (pliki w katalogu roboczym sa wlasnoscia roota)")
		}
		absWorkDir, err := filepath.Abs(workDir)
		if err != nil {
			fail("nieprawidlowa sciezka katalogu roboczego: " + err.Error())
		}
		absOutputISOForClean, err := filepath.Abs(outputISO)
		if err != nil {
			fail("nieprawidlowa sciezka wyjsciowa ISO: " + err.Error())
		}
		runClean(absWorkDir, absOutputISOForClean, cleanAll)
		os.Exit(0)
	}

	// Akceptujemy DWIE formy wywolania:
	//   hackeros-builder build cloud   (positional = ["build", "cloud"])
	//   hackeros-builder cloud         (positional = ["cloud"])
	// Bez tego rozgalezienia "hackeros-builder cloud" (forma intuicyjna,
	// ktorej uzytkownicy oczekuja) cicho spadala do printUsage()+exit(1)
	// bez ZADNEGO wyjasnienia co jest zle -- to byl realny, zglaszany blad.
	var subcommand string
	switch {
	case len(positional) == 2 && positional[0] == "build":
		subcommand = positional[1]
	case len(positional) == 1:
		subcommand = positional[0]
	case len(positional) == 0:
		printUsage()
		os.Exit(1)
	default:
		fail(fmt.Sprintf(
			"nieprawidlowa liczba argumentow (%d) -- oczekiwano 'build <cloud|iso|all>' "+
				"albo skroconej formy '<cloud|iso|all>'. Zobacz --help.",
			len(positional)))
	}

	if !isValidSubcommand(subcommand) {
		fail(fmt.Sprintf(
			"nieznana komenda %q -- poprawne opcje to: cloud, iso, all "+
				"(z opcjonalnym prefiksem 'build'). Zobacz --help.",
			subcommand))
	}

	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		fail("nieprawidlowa sciezka projektu: " + err.Error())
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		fail("nieprawidlowa sciezka katalogu roboczego: " + err.Error())
	}
	absOutputISO, err := filepath.Abs(outputISO)
	if err != nil {
		fail("nieprawidlowa sciezka wyjsciowa ISO: " + err.Error())
	}

	// Sprawdzamy ze config/config.hk faktycznie istnieje w projectDir PRZED
	// wywolaniem buildflow -- gdy uzytkownik odpala z niewlasciwego katalogu
	// (np. wewnatrz "config/" zamiast katalogu nadrzednego), dostaje to
	// natychmiast, z podpowiedzia, a nie po preflight/locku/parsowaniu.
	configPath := filepath.Join(absProjectDir, "config", "config.hk")
	if _, statErr := os.Stat(configPath); statErr != nil {
		fail(fmt.Sprintf(
			"nie znaleziono %s\n"+
				"  Sprawdzane w katalogu projektu: %s\n"+
				"  Jesli odpalasz z wnetrza katalogu 'config/', wyjdz jeden poziom wyzej:\n"+
				"    cd .. && sudo hackeros-builder %s\n"+
				"  albo wskaz katalog projektu explicite:\n"+
				"    sudo hackeros-builder %s --project /sciezka/do/projektu",
			configPath, absProjectDir, subcommand, subcommand))
	}

	if os.Geteuid() != 0 {
		fail("hackeros-builder wymaga uprawnien roota (debootstrap/chroot/mount)")
	}

	switch subcommand {
	case "cloud":
		result, err := buildflow.BuildCloud(buildflow.CloudOptions{
			ProjectDir:       absProjectDir,
			WorkDir:          absWorkDir,
			InsecureRegistry: insecureRegistry,
			SkipPreflight:    skipPreflight,
		})
		if err != nil {
			fail(err.Error())
		}
		fmt.Println()
		fmt.Println(util.Colorize(util.ColorGreen, "Obraz OCI wypchniety:") +
			fmt.Sprintf(" %s:%s", result.Repository, result.Tag))
		fmt.Printf("Origin refspec dla deb-ostree: %s\n", result.Refspec)

	case "iso":
		err := buildflow.BuildIso(buildflow.IsoOptions{
			ProjectDir:       absProjectDir,
			WorkDir:          absWorkDir,
			OutputISO:        absOutputISO,
			InsecureRegistry: insecureRegistry,
			SkipPreflight:    skipPreflight,
			SkipInstaller:    noInstaller,
		})
		if err != nil {
			fail(err.Error())
		}
		fmt.Println()
		fmt.Println(util.Colorize(util.ColorGreen, "ISO zbudowane:") + " " + absOutputISO)

	case "all":
		err := buildflow.BuildAll(buildflow.AllOptions{
			ProjectDir:       absProjectDir,
			WorkDir:          absWorkDir,
			OutputISO:        absOutputISO,
			InsecureRegistry: insecureRegistry,
			SkipInstaller:    noInstaller,
		})
		if err != nil {
			fail(err.Error())
		}
		fmt.Println()
		fmt.Println(util.Colorize(util.ColorGreen, "Build all zakonczony.") + " ISO: " + absOutputISO)
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, util.Colorize(util.ColorRed, "hackeros-builder:")+" "+msg)
	os.Exit(1)
}

// runClean usuwa katalog roboczy (--workdir), a w trybie --all dodatkowo
// plik wyjsciowy .iso (--output). Brak tych sciezek NIE jest bledem --
// "clean" ma byc bezpieczne do wywolania wielokrotnie/"na wszelki wypadek"
// (np. w skryptach CI po kazdym buildzie), wiec idempotentne czyszczenie
// czegos co juz nie istnieje konczy sie po prostu informacja, nie awaria.
func runClean(absWorkDir, absOutputISO string, all bool) {
	if _, err := os.Stat(absWorkDir); err == nil {
		util.Infof("Usuwanie katalogu roboczego: %s", absWorkDir)
		if err := os.RemoveAll(absWorkDir); err != nil {
			fail(fmt.Sprintf("nie mozna usunac katalogu roboczego %s: %v", absWorkDir, err))
		}
	} else {
		util.Infof("Katalog roboczy %s juz nie istnieje -- pomijam", absWorkDir)
	}

	if !all {
		fmt.Println(util.Colorize(util.ColorGreen, "Wyczyszczono.") + " (uzyj 'clean --all' by usunac rowniez wynikowy plik .iso)")
		return
	}

	if _, err := os.Stat(absOutputISO); err == nil {
		util.Infof("Usuwanie wynikowego pliku ISO: %s", absOutputISO)
		if err := os.Remove(absOutputISO); err != nil {
			fail(fmt.Sprintf("nie mozna usunac %s: %v", absOutputISO, err))
		}
	} else {
		util.Infof("Plik ISO %s juz nie istnieje -- pomijam", absOutputISO)
	}

	fmt.Println(util.Colorize(util.ColorGreen, "Wyczyszczono (--all):") + " katalog roboczy i plik ISO")
}
