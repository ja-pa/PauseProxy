package httpapi

import (
	"crypto/sha1" // Windows certificate stores display this legacy identifier as Thumbprint.
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"strings"
)

// Hash the DER certificate, not the PEM file, so formatting and filename do not
// affect comparison with a certificate already installed on another device.
func (a *API) certificateDetails(details map[string]string) {
	block, _ := pem.Decode(a.PublicCert)
	if block == nil || block.Type != "CERTIFICATE" {
		details["ca_error"] = "Certificate fingerprints unavailable."
		return
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		details["ca_error"] = "Certificate fingerprints unavailable."
		return
	}
	sha256Sum := sha256.Sum256(cert.Raw)
	sha1Sum := sha1.Sum(cert.Raw)
	details["ca_sha256"] = strings.ToUpper(hex.EncodeToString(sha256Sum[:]))
	details["ca_sha1"] = strings.ToUpper(hex.EncodeToString(sha1Sum[:]))
}
