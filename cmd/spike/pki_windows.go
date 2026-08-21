//go:build windows

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// keyPassphrase protects the generated client key.
//
// The point of encrypting it is to make openvpn ask for a passphrase over the
// management interface, which is the exact prompt that used to be lost.
var keyPassphrase = "spike-passphrase"

// pki is the set of files a TLS loopback test needs.
type pki struct {
	Dir string
	// KeyPassphrase is what unlocks the client key.
	KeyPassphrase string
}

// generatePKI writes a throwaway CA plus a server and client certificate into
// dir, with the client's private key encrypted.
//
// It exists so the private-key passphrase path can be tested against real
// openvpn without a server, a network, or anyone's credentials. Everything here
// is disposable and must never be used for anything real.
func generatePKI(dir string) (*pki, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	caKey, caCert, caDER, err := makeCA()
	if err != nil {
		return nil, err
	}

	serverKey, serverDER, err := makeLeaf("spike-server", caCert, caKey, x509.ExtKeyUsageServerAuth)
	if err != nil {
		return nil, err
	}
	clientKey, clientDER, err := makeLeaf("spike-client", caCert, caKey, x509.ExtKeyUsageClientAuth)
	if err != nil {
		return nil, err
	}

	// The client key is the only encrypted one. EncryptPEMBlock writes the
	// legacy OpenSSL format, which is what openvpn's OpenSSL build reads.
	encrypted, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY",
		x509.MarshalPKCS1PrivateKey(clientKey), []byte(keyPassphrase), x509.PEMCipherAES256)
	if err != nil {
		return nil, fmt.Errorf("encrypt the client key: %w", err)
	}

	files := map[string][]byte{
		"ca.crt":     pemBlock("CERTIFICATE", caDER),
		"server.crt": pemBlock("CERTIFICATE", serverDER),
		"server.key": pemBlock("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey)),
		"client.crt": pemBlock("CERTIFICATE", clientDER),
		"client.key": pem.EncodeToMemory(encrypted),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
	}

	return &pki{Dir: dir, KeyPassphrase: keyPassphrase}, nil
}

func makeCA() (*rsa.PrivateKey, *x509.Certificate, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "spike-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	return key, cert, der, nil
}

func makeLeaf(cn string, ca *x509.Certificate, caKey *rsa.PrivateKey, usage x509.ExtKeyUsage) (*rsa.PrivateKey, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	return key, der, nil
}

func pemBlock(kind string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
}
