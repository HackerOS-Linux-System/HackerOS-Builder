import os
import osproc
import strutils
import sequtils
import parsecfg  # For parsing config files like /etc/xdg/kcm-about-distrorc and .hacker files
import terminal  # For styled output

# Function to check if the system is Debian Forky or compatible HackerOS
proc checkForkyCompatibility(): bool =
  if fileExists("/etc/os-release"):
    let content = readFile("/etc/os-release")
    if content.contains("PRETTY_NAME=\"Debian GNU/Linux forky\"") or content.contains("VERSION_CODENAME=forky"):
      return true
  if fileExists("/etc/xdg/kcm-about-distrorc"):
    let config = loadConfig("/etc/xdg/kcm-about-distrorc")
    let variant = config.getSectionValue("", "Variant")
    let allowedVariants = ["Official Edition", "Hydra Edition", "Gnome Edition", "Xfce Edition", "Gaming Edition"]
    if variant in allowedVariants:
      return true
  return false

# Function to check if the system is Debian Trixie or compatible HackerOS
proc checkTrixieCompatibility(): bool =
  if fileExists("/etc/os-release"):
    let content = readFile("/etc/os-release")
    if content.contains("PRETTY_NAME=\"Debian GNU/Linux trixie\"") or content.contains("VERSION_CODENAME=trixie"):
      return true
  if fileExists("/etc/xdg/kcm-about-distrorc"):
    let config = loadConfig("/etc/xdg/kcm-about-distrorc")
    let variant = config.getSectionValue("", "Variant")
    let allowedVariants = ["LTS Edition", "Cybersecurity Edition"]
    if variant in allowedVariants:
      return true
  return false

# Function to execute a command and print output
proc execCmd(cmd: string) =
  styledEcho fgGreen, "Executing: ", fgWhite, cmd
  let result = execCmdEx(cmd)
  if result.exitCode != 0:
    styledEcho fgRed, "Error: ", result.output
    quit(1)
  else:
    styledEcho fgCyan, result.output

# Function to process .hacker profile files
proc processHackerProfiles(buildDir: string) =
  styledEcho fgYellow, "Processing .hacker profile files..."
  for file in walkFiles(buildDir / "*.hacker"):
    styledEcho fgCyan, "Found profile file: ", file
    let config = loadConfig(file)
    # Assuming .hacker files have sections like [packages], [hooks], etc.
    # For each section, create corresponding live-build config files
    
    # Example: [packages] -> config/package-lists/<filename>.list.chroot
    let profileName = file.extractFilename().changeFileExt("")
    let packageListDir = buildDir / "config" / "package-lists"
    createDir(packageListDir)
    let packageFile = packageListDir / (profileName & ".list.chroot")
    var packages: seq[string]
    for key, value in config["packages"]:
      if value.len > 0:
        packages.add(value)
    if packages.len > 0:
      writeFile(packageFile, packages.join("\n"))
      styledEcho fgGreen, "Created package list: ", packageFile
    
    # Example: [bootappend] -> add to lb config options
    let bootappend = config.getSectionValue("bootappend", "parameters")
    if bootappend.len > 0:
      styledEcho fgYellow, "Found bootappend parameters: ", bootappend
      # We can collect additional options here, but for now, note it (integrate later if needed)
    
    # Add more sections as needed, e.g., [includes], [hooks], etc.
    # For [includes.chroot]: create config/includes.chroot/ and copy files
    # But since not specified, keeping it basic for packages and example

    # Validate format: sections start with [ and end with ]
    # parsecfg already handles ini format with [sections]

# Function to build in a specified directory
proc buildInDir(buildDir: string, isStable: bool = false, isProfile: bool = false) =
  let originalDir = getCurrentDir()
  setCurrentDir(buildDir)
  defer: setCurrentDir(originalDir)

  # Clear screen
  execCmd("clear")

  # Clean previous builds
  execCmd("sudo lb clean --purge")

  if isProfile:
    processHackerProfiles(buildDir)

  # Config command
  var configCmd = "lb config --architectures amd64 --apt-options \"--allow-unauthenticated --yes\" --firmware-chroot true"
  if isStable:
    configCmd = "lb config --distribution trixie " & configCmd
  else:
    configCmd = "lb config --distribution forky " & configCmd

  # If additional options from profiles, append here
  # For example, if bootappend collected, add "--bootappend-live \"" & bootappend & "\""

  execCmd(configCmd)

  # Build image
  execCmd("sudo lb build")

  # Ask for ISO move and rename
  let isoFile = "live-image-amd64.hybrid.iso"  # Default name from lb build
  if fileExists(isoFile):
    styledEcho fgYellow, "Build complete. ISO file: ", isoFile
    stdout.write "Enter new name for ISO (or press Enter to keep): "
    let newName = readLine(stdin).strip()
    var finalIso = isoFile
    if newName != "":
      finalIso = newName & ".iso"
      moveFile(isoFile, finalIso)
    
    stdout.write "Enter directory to move ISO to (or press Enter to keep here): "
    let moveDir = readLine(stdin).strip()
    if moveDir != "":
      let destPath = moveDir / finalIso.extractFilename()
      moveFile(finalIso, destPath)
      styledEcho fgGreen, "Moved to: ", destPath
  else:
    styledEcho fgRed, "ISO file not found after build!"

  # Clean after build
  execCmd("sudo lb clean --purge")

# Main CLI commands
proc build(here: bool = false, stable: bool = false) =
  if stable:
    if not checkTrixieCompatibility():
      styledEcho fgRed, "Error: Must be on Debian Trixie or compatible HackerOS edition."
      quit(1)
  else:
    if not checkForkyCompatibility():
      styledEcho fgRed, "Error: Must be on Debian Forky or compatible HackerOS edition."
      quit(1)

  var buildDir = getCurrentDir()
  if not here:
    stdout.write "Enter directory to build in: "
    buildDir = readLine(stdin).strip()
    if not dirExists(buildDir):
      styledEcho fgRed, "Error: Directory does not exist."
      quit(1)

  buildInDir(buildDir, stable)

proc profile() =
  if not checkForkyCompatibility():
    styledEcho fgRed, "Error: Must be on Debian Forky or compatible HackerOS edition."
    quit(1)

  stdout.write "Enter directory to build profile in: "
  let buildDir = readLine(stdin).strip()
  if not dirExists(buildDir):
    styledEcho fgRed, "Error: Directory does not exist."
    quit(1)

  buildInDir(buildDir, isProfile = true)

when isMainModule:
  import cligen
  dispatchMulti(
    [build, help = {"here": "Build in current directory", "stable": "Build stable version (Trixie)"}],
    [profile]
  )
