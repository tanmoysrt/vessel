package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/nats-io/nats.go"
	"golang.org/x/net/idna"
	"net"
	"regexp"
	"slices"
	"strings"
	"time"
)

func ReadAllMessagesOfChannel(ch chan *nats.Msg) []*nats.Msg {
	messages := make([]*nats.Msg, 0, len(ch)) // preallocate based on current buffer size

	for {
		select {
		case msg := <-ch:
			messages = append(messages, msg)
		default:
			// channel empty
			return messages
		}
	}
}

func ValidateCertAndKey(certPEM, keyPEM string) error {
	// Decode certificate
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("invalid certificate PEM data")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %v", err)
	}

	// Decode private key
	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		return errors.New("invalid private key PEM data")
	}

	var privKey interface{}
	switch keyBlock.Type {
	case "RSA PRIVATE KEY":
		privKey, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	case "PRIVATE KEY":
		privKey, err = x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	default:
		return fmt.Errorf("unsupported private key type: %s", keyBlock.Type)
	}
	if err != nil {
		return fmt.Errorf("failed to parse private key: %v", err)
	}

	// Compare public keys
	switch key := privKey.(type) {
	case *rsa.PrivateKey:
		rsaPub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return errors.New("certificate public key is not RSA")
		}
		if rsaPub.N.Cmp(key.PublicKey.N) != 0 {
			return errors.New("certificate and key do not match")
		}
	default:
		return errors.New("unsupported private key type for validation")
	}

	return nil
}

func getCertExpiry(certPEM string) (time.Time, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return time.Time{}, errors.New("invalid certificate PEM data")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse certificate: %v", err)
	}

	return cert.NotAfter, nil
}

const maxDomainLen = 253

var labelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// IsValidDomain checks whether a given string is a valid domain name.
// It returns (true, nil) if valid, or (false, error) otherwise.
//
// Rules:
//   - "." is NOT allowed
//   - "*" and "*.<domain>" are allowed (wildcard only as entire leftmost label)
//   - Optional trailing dot allowed
//   - Supports IDNs
//   - Each label 1–63 chars, no leading/trailing hyphen
//   - Total length ≤ 253 (excluding trailing dot)
func IsValidDomain(s string) (bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return false, errors.New("empty domain")
	}
	if s == "." {
		return false, errors.New("root domain '.' is not allowed")
	}

	// Allow trailing dot and strip it for validation
	if strings.HasSuffix(s, ".") {
		s = strings.TrimSuffix(s, ".")
		if s == "" {
			return false, errors.New("domain cannot be only a trailing dot")
		}
	}

	// Wildcard handling
	if s == "*" {
		return true, nil
	}
	if strings.HasPrefix(s, "*.") {
		rest := s[2:]
		if rest == "" {
			return false, errors.New("'*.' must be followed by a domain")
		}
		return validateDomain(rest)
	}

	return validateDomain(s)
}

func validateDomain(s string) (bool, error) {
	ascii, err := idna.Lookup.ToASCII(s)
	if err != nil {
		return false, errors.New("invalid IDN: " + err.Error())
	}
	if len(ascii) > maxDomainLen {
		return false, errors.New("domain exceeds 253 characters")
	}
	if strings.Contains(ascii, "..") || strings.HasPrefix(ascii, ".") || strings.HasSuffix(ascii, ".") {
		return false, errors.New("labels must be separated by a single dot")
	}

	parts := strings.Split(ascii, ".")
	for _, p := range parts {
		if len(p) == 0 {
			return false, errors.New("empty label")
		}
		if strings.ContainsRune(p, '_') {
			return false, errors.New("underscores are not allowed in labels")
		}
		if !labelRE.MatchString(p) {
			return false, errors.New("invalid label: " + p)
		}
	}
	return true, nil
}

func IsValidCIDR(s string) bool {
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

func uniqueSortedStrings(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}
