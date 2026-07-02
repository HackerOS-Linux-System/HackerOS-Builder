package httpclient

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// DefaultTimeout to maksymalny czas na CALE zadanie HTTP (polaczenie +
// wyslanie + odebranie odpowiedzi) -- wystarczajacy dla zapytan API
// (GitHub releases) i pobierania pojedynczych plikow (binarka deb-ostree).
const DefaultTimeout = 30 * time.Second

// RegistryTimeout to dluzszy timeout na bezczynnosc polaczenia przy
// operacjach na registry OCI (push/pull warstw moga byc wieloMB/GB).
// To NIE jest limit na cala operacje -- duzy plik wciaz przesylajacy dane
// nie zostanie przerwany, tylko polaczenie ktore przestalo odpowiadac.
const RegistryTimeout = 5 * time.Minute

// New zwraca klienta HTTP z rozsadnym, ograniczonym timeoutem -- do uzycia
// przy krotkich zadaniach (sprawdzanie najnowszej wersji, pobieranie
// pojedynczego pliku binarnego typu deb-ostree).
func New() *http.Client {
	return &http.Client{
		Timeout: DefaultTimeout,
	}
}

// NewForRegistry zwraca klienta HTTP dostosowanego do pracy z registry OCI
// (duze transfery, dluzszy budzet czasowy na bezczynnosc polaczenia).
// insecureSkipVerify=true wylacza weryfikacje certyfikatu TLS -- uzywane
// TYLKO gdy uzytkownik explicite poprosi o to dla self-signed/insecure
// registry (patrz internal/ociimage, opcja Insecure).
func NewForRegistry(insecureSkipVerify bool) *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: RegistryTimeout,
		IdleConnTimeout:       90 * time.Second,
	}

	if insecureSkipVerify {
		// Jawna decyzja uzytkownika (np. prywatny registry testowy bez
		// poprawnego certyfikatu) -- nigdy nie wlaczane domyslnie.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &http.Client{
		Transport: transport,
	}
}
