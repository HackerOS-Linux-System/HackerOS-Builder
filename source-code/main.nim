import std/[os, json, strutils, terminal, osproc]

proc determineDistribution(version: string): string =
  let v = version.toLowerAscii()
  case v
  of "lts":
    result = "trixie"
  of "normal":
    result = "forky"
  else:
    raise newException(ValueError, "Nieznana wersja: " & version & ". Obsługiwane: 'lts' lub 'normal'.")

proc main() =
  styledEcho(styleBright, fgBlue, "Inicjowanie budowania HackerOS...")

  let configFile = "config-hackeros.hacker"
  let configDir = "config"

  if not fileExists(configFile):
    styledEcho(styleBright, fgRed, "Błąd: Plik konfiguracyjny '" & configFile & "' nie istnieje w bieżącym katalogu.")
    quit(1)

  if not dirExists(configDir):
    styledEcho(styleBright, fgRed, "Błąd: Folder konfiguracyjny '" & configDir & "' nie istnieje w bieżącym katalogu.")
    quit(1)

  let content = readFile(configFile).strip()

  var configJson: JsonNode
  try:
    configJson = parseJson(content)
  except JsonParsingError as e:
    styledEcho(styleBright, fgRed, "Błąd parsowania pliku konfiguracyjnego: " & e.msg)
    quit(1)

  if configJson.kind != JArray or configJson.len < 1 or configJson[0].kind != JString:
    styledEcho(styleBright, fgRed, "Nieprawidłowy format pliku konfiguracyjnego. Oczekiwano tablicy z co najmniej jednym ciągiem znaków, np. [\"lts\"].")
    quit(1)

  let version = configJson[0].getStr()

  var dist: string
  try:
    dist = determineDistribution(version)
  except ValueError as e:
    styledEcho(styleBright, fgRed, "Błąd: " & e.msg)
    quit(1)

  styledEcho(styleBright, fgGreen, "Budowanie HackerOS na bazie Debiana (dystrybucja: " & dist & ")...")

  styledEcho(styleDim, fgYellow, "Czyszczenie poprzednich buildów (lb clean)...")
  let cleanResult = execCmdEx("lb clean")
  if cleanResult.exitCode != 0:
    styledEcho(styleBright, fgRed, "Błąd podczas czyszczenia: " & cleanResult.output)
    quit(1)

  styledEcho(styleDim, fgYellow, "Konfiguracja live-build (lb config --distribution " & dist & ")...")
  let configCmd = "lb config --distribution " & dist
  let configResult = execCmdEx(configCmd)
  if configResult.exitCode != 0:
    styledEcho(styleBright, fgRed, "Błąd podczas konfiguracji live-build: " & configResult.output)
    quit(1)

  styledEcho(styleDim, fgYellow, "Budowanie obrazu (lb build)...")
  let buildResult = execCmdEx("lb build")
  if buildResult.exitCode != 0:
    styledEcho(styleBright, fgRed, "Błąd podczas budowania obrazu: " & buildResult.output)
    quit(1)

  styledEcho(styleBright, fgGreen, "Budowanie zakończone sukcesem. Obraz HackerOS powinien być dostępny w bieżącym katalogu.")

when isMainModule:
  main()
