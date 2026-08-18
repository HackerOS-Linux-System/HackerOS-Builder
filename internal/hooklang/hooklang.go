package hooklang

// interpreterPackages mapuje nazwe binarki interpretera (dokladnie tak jak
// pojawia sie po "#!/usr/bin/env " albo "#!/usr/bin/") na pakiet apt
// dostarczajacy ta binarke w domyslnych repozytoriach Debiana (main).
//
// sh/bash/dash/ash CELOWO nie sa tu wpisane -- sa czescia kazdego systemu
// Debian od razu po debootstrap (dash jako /bin/sh, bash z pakietu "bash"
// ktory debootstrap instaluje w kazdym wariancie), wiec proba
// "doinstalowania" ich bylaby zbednym apt-get install za kazdym razem.
var interpreterPackages = map[string]string{
	"python3": "python3",
	// Debian trixie/sid nie ma juz pakietu "python" (python2) w main --
	// najblizszy odpowiednik dla skryptow z "#!/usr/bin/env python" to
	// python-is-python3 (dostarcza symlink /usr/bin/python -> python3).
	"python":  "python-is-python3",
	"python2": "python-is-python3",
	"ruby":    "ruby",
	"ruby3":   "ruby",
	"ruby3.1": "ruby",
	"ruby3.2": "ruby",
	"lua":     "lua5.4",
	"lua5.1":  "lua5.1",
	"lua5.3":  "lua5.3",
	"lua5.4":  "lua5.4",
	"perl":    "perl",
	"node":    "nodejs",
	"nodejs":  "nodejs",
	"php":     "php-cli",
	"php-cli": "php-cli",
	"php8":    "php-cli",
	"tclsh":   "tcl",
	"tcl":     "tcl",
	"Rscript": "r-base-core",
	"R":       "r-base-core",
	"gawk":    "gawk",
	"awk":     "gawk",
	"fish":    "fish",
	"zsh":     "zsh",
	"ksh":     "ksh",
}

// alwaysPresent to interpretery ktore sa czescia bazowego systemu Debian
// zaraz po debootstrap -- InterpreterPackage zwraca dla nich ok=false
// (nic do zainstalowania), zeby caller nie marnowal czasu na apt-get
// install pakietu ktory i tak juz jest.
var alwaysPresent = map[string]bool{
	"":     true, // brak shebang -- nie ma czego instalowac (patrz Label)
	"sh":   true,
	"bash": true,
	"dash": true,
	"ash":  true,
}

// InterpreterPackage zwraca nazwe pakietu apt dostarczajacego dany
// interpreter i true jesli jest rozpoznany i WYMAGA instalacji. Zwraca
// ("", false) zarowno dla interpreterow zawsze obecnych (sh/bash/...) jak i
// dla nierozpoznanych -- w drugim przypadku wywolujacy powinien user
// ostrzec zamiast cicho kontynuowac (patrz RequiresInstall).
func InterpreterPackage(interpreter string) (pkg string, ok bool) {
	if alwaysPresent[interpreter] {
		return "", false
	}
	pkg, known := interpreterPackages[interpreter]
	return pkg, known
}

// IsRecognized zwraca true jesli interpreter jest ROZPOZNANY przez ta
// mape (niezaleznie od tego, czy wymaga instalacji) -- odroznia "znany
// jezyk, juz obecny w systemie" (sh/bash) od "kompletnie nieznany
// interpreter" (literowka w shebang, albo jezyk spoza obecnie wspieranej
// listy), co pozwala wywolujacemu dac uzytkownikowi uzyteczne ostrzezenie
// tylko w tym drugim przypadku.
func IsRecognized(interpreter string) bool {
	if alwaysPresent[interpreter] {
		return true
	}
	_, known := interpreterPackages[interpreter]
	return known
}

// Label zwraca czytelna etykiete jezyka hooka do logow CLI.
func Label(interpreter string) string {
	if interpreter == "" {
		return "brak shebang/binarny"
	}
	return interpreter
}

// SupportedLanguages zwraca posortowana, czytelna liste wspieranych
// jezykow (z automatyczna instalacja interpretera) do komunikatow
// bledow/dokumentacji -- np. gdy shebang wskazuje na cos nierozpoznanego,
// blad moze podpowiedziec co JEST obslugiwane "od reki".
func SupportedLanguages() []string {
	return []string{
		"sh / bash / dash (wbudowane w kazdy system Debian)",
		"python3 (oraz \"python\" -> python-is-python3)",
		"ruby",
		"lua5.1 / lua5.3 / lua5.4",
		"perl",
		"nodejs (\"node\")",
		"php-cli (\"php\")",
		"tcl",
		"r-base-core (\"Rscript\")",
		"gawk",
		"fish / zsh / ksh",
	}
}
