package mitm

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestAdminCertificateTrustedForConfiguredIP(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	if err := Bootstrap(dir); err != nil {
		t.Fatal(err)
	}
	ca, err := LoadCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := ca.GetCert("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.GetRootCA())
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if err = leaf.VerifyHostname("192.168.1.50"); err == nil {
		t.Fatal("incorrect host accepted")
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*pair}}
	server.StartTLS()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "ok" {
		t.Fatal("HTTPS failed")
	}
}
