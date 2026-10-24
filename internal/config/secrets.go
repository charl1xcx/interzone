package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// RandomPassword returns n characters that are safe to drop into YAML and shells.
func RandomPassword(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b[i] = alphabet[v.Int64()]
	}
	return string(b)
}

func RandomHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// SelfSignedCert writes a wildcard certificate for domain and *.domain into
// dir, reusing an existing pair so re-runs don't rotate it.
func SelfSignedCert(dir, domain string) (certPath, keyPath string, err error) {
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "privkey.pem")
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return certPath, keyPath, nil
		}
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: domain, Organization: []string{"interzone self-signed"}},
		DNSNames:              []string{domain, "*." + domain, "*.element." + domain},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return
	}
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return
	}
	err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return
}
