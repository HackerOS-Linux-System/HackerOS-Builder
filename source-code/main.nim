require "json"

def determine_distribution(version : String) : String
  case version.downcase
  when "lts"
    "trixie"
  when "normal"
    "forky"
  else
    raise "Nieznana wersja: #{version}. Obsługiwane: 'lts' lub 'normal'."
  end
end

def main
  config_file = "config-hackeros.hacker"
  config_dir = "config"

  unless File.exists?(config_file)
    puts "Błąd: Plik konfiguracyjny '#{config_file}' nie istnieje w bieżącym katalogu."
    exit(1)
  end

  unless Dir.exists?(config_dir)
    puts "Błąd: Folder konfiguracyjny '#{config_dir}' nie istnieje w bieżącym katalogu."
    exit(1)
  end

  content = File.read(config_file).strip
  begin
    config_array = JSON.parse(content).as_a
    unless config_array.size >= 1 && config_array[0].as_s?
      raise "Nieprawidłowy format pliku konfiguracyjnego. Oczekiwano tablicy z co najmniej jednym ciągiem znaków, np. [\"lts\"]."
    end
    version = config_array[0].as_s
  rescue ex : JSON::ParseException | TypeCastError
    puts "Błąd parsowania pliku konfiguracyjnego: #{ex.message}"
    exit(1)
  end

  begin
    dist = determine_distribution(version)
  rescue ex
    puts "Błąd: #{ex.message}"
    exit(1)
  end

  puts "Budowanie HackerOS na bazie Debiana (dystrybucja: #{dist})..."

  # Uruchom lb clean, aby oczyścić poprzednie buildy
  Process.run("lb", args: ["clean"], shell: true, error: STDERR, output: STDOUT)

  # Uruchom lb config z wybraną dystrybucją
  config_cmd = "lb config --distribution #{dist}"
  unless system(config_cmd)
    puts "Błąd podczas konfiguracji live-build."
    exit(1)
  end

  # Uruchom lb build
  unless system("lb build")
    puts "Błąd podczas budowania obrazu."
    exit(1)
  end

  puts "Budowanie zakończone sukcesem. Obraz HackerOS powinien być dostępny w bieżącym katalogu."
end

main if __FILE__ == Process.executable_path
