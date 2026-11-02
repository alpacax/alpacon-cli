package cert

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/utils"
)

func generateKey(keyPath string) (*rsa.PrivateKey, error) {
	if _, err := os.Stat(keyPath); err == nil {
		key, err := readPrivateKey(keyPath)
		if err != nil {
			return nil, err
		}
		return key, nil
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	err = savePrivateKey(keyPath, key)
	if err != nil {
		return nil, err
	}

	return key, nil
}

func CreateCSR(res cert.SignRequestResponse, certPath CertificatePath) ([]byte, error) {
	subject := pkix.Name{
		Organization: []string{res.Organization},
		CommonName:   res.CommonName,
	}

	template := &x509.CertificateRequest{
		Subject:            subject,
		SignatureAlgorithm: x509.SHA256WithRSA,
		DNSNames:           res.DomainList,
		IPAddresses:        parseNetIP(res.IpList),
	}

	privateKey, err := generateKey(certPath.PrivateKeyPath)
	if err != nil {
		return nil, err
	}

	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, template, privateKey)
	if err != nil {
		return nil, err
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrBytes,
	})

	err = utils.SaveFile(certPath.CSRPath, csrPEM)
	if err != nil {
		return nil, err
	}

	return csrPEM, nil
}

func readPrivateKey(keyPath string) (*rsa.PrivateKey, error) {
	pemBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("failed to find PEM block in the key file")
	}
	if block.Type != "RSA PRIVATE KEY" {
		return nil, fmt.Errorf("invalid key type: %s, expected 'RSA PRIVATE KEY'", block.Type)
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %v", err)
	}

	return key, nil
}

func parseNetIP(ipList []string) []net.IP {
	var ipAddresses []net.IP
	for _, ipStr := range ipList {
		ip := net.ParseIP(ipStr)
		if ip != nil {
			ipAddresses = append(ipAddresses, ip)
		}
	}

	return ipAddresses
}

func savePrivateKey(fileName string, key *rsa.PrivateKey) error {
	dir := filepath.Dir(fileName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directories: %v", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	// Staged in a temp file and renamed, so a write that fails partway never leaves a corrupt key
	// for generateKey to pick back up.
	if _, err := utils.SaveStreamAtomic(fileName, bytes.NewReader(keyPEM), 0600); err != nil {
		return fmt.Errorf("failed to save private key file: %v", err)
	}

	return nil
}
