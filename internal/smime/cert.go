package smime

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// parseCertificateFromPEM parses the first certificate from PEM-encoded data
func parseCertificateFromPEM(pemData string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("no PEM data found")
	}
	return x509.ParseCertificate(block.Bytes)
}

// parseCertChainFromPEM parses all certificates from PEM-encoded data
func ParseCertChainFromPEM(pemData string) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := []byte(pemData)

	for len(rest) > 0 {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse certificate: %w", err)
		}
		certs = append(certs, cert)
	}

	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificates found in PEM data")
	}
	return certs, nil
}

// certificateFingerprint returns the SHA-256 fingerprint of a DER-encoded certificate
func certificateFingerprint(derBytes []byte) string {
	sum := sha256.Sum256(derBytes)
	return fmt.Sprintf("%x", sum)
}

// parseCertificateFromDER parses a DER-encoded X.509 certificate
func parseCertificateFromDER(derBytes []byte) (*x509.Certificate, error) {
	return x509.ParseCertificate(derBytes)
}

// extractEmailFromCert extracts the email address from a certificate's SAN or Subject
func extractEmailFromCert(cert *x509.Certificate) string {
	// Check Subject Alternative Names first
	for _, email := range cert.EmailAddresses {
		return email
	}
	return ""
}
