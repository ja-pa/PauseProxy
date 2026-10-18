package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http/httptest"
	"pauseproxy/internal/config"
	"strings"
	"testing"
)

func TestConnectionCertificateFingerprints(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	a := &API{Config: config.Default()}
	get := func() map[string]string {
		t.Helper()
		// No login or device registration required to view public fingerprints.
		r := httptest.NewRequest("GET", a.Config.AdminURL+"/connection", nil)
		r.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		a.Admin(w, r)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("connection: %d %v", w.Code, w.Header())
		}
		var details map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &details); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(details["ca"], "/ca-cert.pem") || details["pac"] == "" || details["demo"] == "" {
			t.Fatal("connection addresses missing", details)
		}
		return details
	}
	for _, data := range [][]byte{certPEM, []byte("\r\n" + strings.ReplaceAll(string(certPEM), "\n", "\r\n"))} {
		a.PublicCert = data
		details := get()
		if details["ca_sha256"] != fmt.Sprintf("%X", sha256.Sum256(der)) || details["ca_sha1"] != fmt.Sprintf("%X", sha1.Sum(der)) || details["ca_error"] != "" {
			t.Fatal("fingerprints must hash DER independently of PEM formatting", details)
		}
	}
	template.SerialNumber = big.NewInt(2)
	changed, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	a.PublicCert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: changed})
	if get()["ca_sha256"] == fmt.Sprintf("%X", sha256.Sum256(der)) {
		t.Fatal("changed certificate kept old fingerprint")
	}
	for _, data := range [][]byte{nil, []byte("invalid"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid DER")}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})} {
		a.PublicCert = data
		details := get()
		if details["ca_error"] == "" || details["ca_sha256"] != "" || details["ca_sha1"] != "" {
			t.Fatal("invalid certificate advertised a fingerprint", details)
		}
	}
}
