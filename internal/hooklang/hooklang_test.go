package hooklang

import "testing"

func TestInterpreterPackage_AlwaysPresentNeedsNoInstall(t *testing.T) {
	for _, interp := range []string{"", "sh", "bash", "dash", "ash"} {
		pkg, ok := InterpreterPackage(interp)
		if ok {
			t.Errorf("InterpreterPackage(%q) = (%q, true), chcialem ok=false (zawsze obecny w Debianie)", interp, pkg)
		}
	}
}

func TestInterpreterPackage_KnownLanguages(t *testing.T) {
	cases := map[string]string{
		"python3": "python3",
		"python":  "python-is-python3",
		"ruby":    "ruby",
		"lua5.4":  "lua5.4",
		"perl":    "perl",
		"node":    "nodejs",
		"php":     "php-cli",
	}
	for interp, wantPkg := range cases {
		pkg, ok := InterpreterPackage(interp)
		if !ok {
			t.Errorf("InterpreterPackage(%q): ok=false, chcialem true", interp)
			continue
		}
		if pkg != wantPkg {
			t.Errorf("InterpreterPackage(%q) = %q, chcialem %q", interp, pkg, wantPkg)
		}
	}
}

func TestInterpreterPackage_UnknownInterpreter(t *testing.T) {
	pkg, ok := InterpreterPackage("cokolwiek-nieistniejacego")
	if ok {
		t.Errorf("InterpreterPackage(nieznany) = (%q, true), chcialem ok=false", pkg)
	}
}

func TestIsRecognized(t *testing.T) {
	if !IsRecognized("sh") {
		t.Error("IsRecognized(sh) powinno byc true (zawsze obecny)")
	}
	if !IsRecognized("python3") {
		t.Error("IsRecognized(python3) powinno byc true (znany, wymaga instalacji)")
	}
	if IsRecognized("cokolwiek-nieistniejacego") {
		t.Error("IsRecognized(nieznany) powinno byc false")
	}
}

func TestLabel(t *testing.T) {
	if got := Label(""); got == "" {
		t.Error("Label(\"\") nie powinno byc pustym stringiem (brak shebang powinien miec czytelna etykiete)")
	}
	if got := Label("python3"); got != "python3" {
		t.Errorf("Label(python3) = %q, chcialem \"python3\"", got)
	}
}

func TestSupportedLanguages_NotEmpty(t *testing.T) {
	langs := SupportedLanguages()
	if len(langs) == 0 {
		t.Fatal("SupportedLanguages() zwrocilo pusta liste")
	}
}
