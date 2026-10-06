package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type CA struct {
	mu    sync.Mutex
	root  *x509.Certificate
	key   *ecdsa.PrivateKey
	cache map[string]*tls.Certificate
}

func serial() *big.Int {
	v, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		panic(e)
	}
	return v
}
func Bootstrap(dir string) error {
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		return fmt.Errorf("CA directory exists; refusing to overwrite: %s", dir)
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	now := time.Now()
	root := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "PauseProxy Development CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(3, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, e := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if e != nil {
		return e
	}
	priv, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if runtime.GOOS == "windows" {
		u, err := user.Current()
		if err != nil {
			return err
		}
		if out, err := exec.Command("icacls", dir, "/inheritance:r", "/grant:r", u.Username+":(OI)(CI)F").CombinedOutput(); err != nil {
			return fmt.Errorf("restrict CA directory ACL: %w: %s", err, out)
		}
	}
	if e = os.WriteFile(filepath.Join(dir, "ca-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: priv}), 0600); e != nil {
		return e
	}
	return os.WriteFile(filepath.Join(dir, "ca-cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644)
}
func LoadCA(dir string) (*CA, error) {
	certPEM, e := os.ReadFile(filepath.Join(dir, "ca-cert.pem"))
	if e != nil {
		return nil, fmt.Errorf("CA missing: run bootstrap-ca first: %w", e)
	}
	keyPEM, e := os.ReadFile(filepath.Join(dir, "ca-key.pem"))
	if e != nil {
		return nil, e
	}
	pair, e := tls.X509KeyPair(certPEM, keyPEM)
	if e != nil {
		return nil, e
	}
	root, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return nil, e
	}
	key, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok || !root.IsCA {
		return nil, fmt.Errorf("invalid development CA")
	}
	if time.Now().After(root.NotAfter) {
		return nil, fmt.Errorf("CA expired")
	}
	return &CA{root: root, key: key, cache: map[string]*tls.Certificate{}}, nil
}
func (c *CA) GetRootCA() *x509.Certificate { return c.root }
func (c *CA) GetCert(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v := c.cache[host]; v != nil {
		return v, nil
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	now := time.Now()
	v := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: host}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(0, 1, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(host); ip != nil {
		v.IPAddresses = []net.IP{ip}
	} else {
		v.DNSNames = []string{host}
	}
	der, e := x509.CreateCertificate(rand.Reader, v, c.root, &key.PublicKey, c.key)
	if e != nil {
		return nil, e
	}
	pair := &tls.Certificate{Certificate: [][]byte{der, c.root.Raw}, PrivateKey: key}
	if len(c.cache) >= 128 {
		c.cache = map[string]*tls.Certificate{}
	}
	c.cache[host] = pair
	return pair, nil
}
