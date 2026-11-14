package utils

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// LoadKey reads <dir>/private.pem (PKCS#1 or PKCS#8). When it is missing, a 2048-bit key is generated and written
// there, so a fresh checkout starts (the JWKS is built from it).
func LoadKey(dir string) (*rsa.PrivateKey, error) {
	path := filepath.Join(dir, "private.pem")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return generateKey(dir)
	}
	if err != nil {
		return nil, err
	}
	return ParseKey(data, path)
}

// ParseKey reads a PEM private key (PKCS#1 or PKCS#8 RSA); path labels errors.
func ParseKey(data []byte, path string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%s: no PEM block", path)
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an RSA key", path)
	}
	return rk, nil
}

// generateKey creates a 2048-bit key and writes it to dir/private.pem.
func generateKey(dir string) (*rsa.PrivateKey, error) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	priv := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	if err := os.WriteFile(filepath.Join(dir, "private.pem"), priv, 0o600); err != nil {
		return nil, err
	}
	log.Printf("generated a new RSA key in %s", dir)
	return k, nil
}

// KeyID is a short, stable id for the public key (JWT "kid", JWKS).
func KeyID(k *rsa.PublicKey) string {
	der, _ := x509.MarshalPKIXPublicKey(k)
	h := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(h[:12])
}
