package isobuild

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HackerOS-Linux-System/hackeros-builder/internal/hooklang"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/liveparse"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/sandbox"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/toolchain"
	"github.com/HackerOS-Linux-System/hackeros-builder/internal/util"
)

// runInstallerHooks wykonuje hooki z config/hooks/installer/ (patrz
// liveparse.Project.InstallerHooks / liveparse.ParseInstallerHooks) wewnatrz
// rootfsDir -- WYLACZNIE w tej kopii uzywanej do budowy ISO, PO tym jak
// Calamares zostal juz wstrzykniety i skonfigurowany (patrz InjectInstaller
// wolane wczesniej w Build()), zeby hooki mogly nadpisac/rozszerzyc jego
// konfiguracje: wlasny branding.desc, dodatkowe moduly Calamares, wlasny
// wallpaper/logo, cokolwiek innego pod /etc/calamares.
//
// Analogicznie do internal/rootfs.Builder.ensureHookInterpreters/runHooks:
// kazdy jezyk wspierany przez internal/hooklang (python3, ruby, lua, perl,
// nodejs, php, tcl, R, gawk, ...) dostaje automatycznie doinstalowany
// interpreter PRZED wykonaniem hookow (jednym wywolaniem apt-get, dla
// wszystkich brakujacych naraz) -- dokladnie ta sama zasada co dla
// hooks/normal i hooks/live, tylko na innej kopii rootfs i w innym momencie
// przeplywu budowy.
func runInstallerHooks(rootfsDir, workDir string, hooks []liveparse.HookScript) error {
	if len(hooks) == 0 {
		return nil
	}

	tc := toolchain.New(workDir)
	tcEnv := tc.Env()

	if err := ensureInstallerHookInterpreters(rootfsDir, tcEnv, hooks); err != nil {
		return fmt.Errorf("przygotowanie interpreterow hookow instalatora: %w", err)
	}

	bar := util.NewProgressBar("hooki instalatora", int64(len(hooks)), "hookow")
	for i, h := range hooks {
		util.SubStep("[%d/%d] %s  %s", i+1, len(hooks), h.Name,
			util.Colorize(util.ColorMagenta, "("+hooklang.Label(h.Interpreter)+")"))

		tmpName := "/tmp-hackeros-installer-hook-" + h.Name
		destOnHost := filepath.Join(rootfsDir, tmpName)
		if err := copyFile(h.Path, destOnHost); err != nil {
			bar.Fail(h.Name)
			return fmt.Errorf("kopiowanie hooka instalatora %s: %w", h.Name, err)
		}
		if err := os.Chmod(destOnHost, 0o755); err != nil {
			bar.Fail(h.Name)
			os.Remove(destOnHost)
			return fmt.Errorf("chmod hooka instalatora %s: %w", h.Name, err)
		}

		err := sandbox.ExecHook(rootfsDir, tmpName)
		os.Remove(destOnHost)
		if err != nil {
			bar.Fail(h.Name)
			if h.Interpreter != "" && !hooklang.IsRecognized(h.Interpreter) {
				return fmt.Errorf(
					"wykonanie hooka instalatora %s (interpreter %q z shebang) nie powiodlo sie: %w -- "+
						"%q NIE jest jednym z jezykow z automatyczna instalacja interpretera; "+
						"jesli to prawidlowy interpreter, doinstaluj go recznie we wczesniejszym "+
						"hooku instalatora (numeracja prefiksow decyduje o kolejnosci). "+
						"Jezyki z automatyczna instalacja: %s",
					h.Name, h.Interpreter, err, h.Interpreter, strings.Join(hooklang.SupportedLanguages(), "; "))
			}
			return fmt.Errorf("wykonanie hooka instalatora %s: %w", h.Name, err)
		}
		bar.Add(1)
	}
	bar.Finish()
	return nil
}

// ensureInstallerHookInterpreters -- patrz komentarz przy
// internal/rootfs.Builder.ensureHookInterpreters, ta sama logika, tylko
// wywolywana bezposrednio przez sandbox.ExecEnv (isobuild nie ma struktury
// Builder z metoda sandboxExec) z tcEnv toolchainu przekazanym przez
// wywolujacego, zeby apt-get/dpkg z ewentualnie tymczasowo pobranego
// toolchainu byly widoczne w PATH tak samo jak przy InjectInstaller.
func ensureInstallerHookInterpreters(rootfsDir string, tcEnv []string, hooks []liveparse.HookScript) error {
	needed := make(map[string]bool)
	var order []string
	for _, h := range hooks {
		pkg, ok := hooklang.InterpreterPackage(h.Interpreter)
		if !ok {
			continue
		}
		if !needed[pkg] {
			needed[pkg] = true
			order = append(order, pkg)
		}
	}
	if len(order) == 0 {
		return nil
	}

	sort.Strings(order)
	util.Infof("  interpretery hookow instalatora: instalacja %s...", strings.Join(order, ", "))
	args := append([]string{
		"install", "-y",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, order...)
	if err := sandbox.ExecEnv(rootfsDir, tcEnv, "apt-get", args...); err != nil {
		return fmt.Errorf("instalacja interpreterow hookow instalatora (%v): %w", order, err)
	}
	return nil
}
