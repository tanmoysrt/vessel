package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/nats-io/nats.go"
	"golang.org/x/net/idna"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"net"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ReadAllMessagesOfChannel drains all available messages from a NATS channel
// without blocking. Returns immediately when the channel is empty.
func ReadAllMessagesOfChannel(ch chan *nats.Msg) []*nats.Msg {
	noOfMessagesInChannel := len(ch)

	if noOfMessagesInChannel == 0 {
		// Early return if the channel is empty.
		return []*nats.Msg{}
	}

	noOfMessagesRead := 0
	messages := make([]*nats.Msg, 0, noOfMessagesInChannel)

	for {
		// If read all messages, return the messages read so far.
		if noOfMessagesRead >= noOfMessagesInChannel {
			return messages
		}

		select {
		case msg := <-ch:
			// Keep reading messages from the channel until we get all the existing messages.
			noOfMessagesRead += 1
			messages = append(messages, msg)
		default:
			// If no messages are available, return the messages read so far.
			return messages
		}
	}
}

// =========================
// TLS Certificate Utility
// =========================

// ValidateCertAndKey verifies that a PEM-encoded certificate and private key match.
// It checks that:
//   - Both are valid PEM format
//   - Certificate can be parsed
//   - Private key can be parsed (supports RSA PKCS1 and PKCS8)
//   - Public key from cert matches public key derived from a private key
func ValidateCertAndKey(certPEM, keyPEM string) error {
	// Decode and parse certificate
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("invalid certificate PEM data")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %v", err)
	}

	// Decode and parse private key
	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		return errors.New("invalid private key PEM data")
	}

	var privateKey interface{}
	switch keyBlock.Type {
	case "RSA PRIVATE KEY":
		privateKey, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	case "PRIVATE KEY":
		privateKey, err = x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	default:
		return fmt.Errorf("unsupported private key type: %s (expected RSA PRIVATE KEY or PRIVATE KEY)", keyBlock.Type)
	}
	if err != nil {
		return fmt.Errorf("failed to parse private key: %v", err)
	}

	// Verify certificate and key match by comparing public keys
	switch key := privateKey.(type) {
	case *rsa.PrivateKey:
		rsaPub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return errors.New("certificate public key is not RSA")
		}
		if rsaPub.N.Cmp(key.PublicKey.N) != 0 {
			return errors.New("certificate and private key do not match")
		}
	default:
		return errors.New("only RSA private keys are supported for validation")
	}

	return nil
}

// GetCertExpiry extracts the expiration time from a PEM-encoded certificate
func GetCertExpiry(certPEM string) (time.Time, error) {
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

// ============================================================================
// Domain Validation
// ============================================================================

const (
	maxDomainLength = 253 // RFC 1035
	maxLabelLength  = 63  // RFC 1035
)

// Regex for valid DNS labels (lowercase alphanumeric + hyphens, no leading/trailing hyphen)
var domainLabelRegex = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// IsValidDomain validates a domain name according to RFC 1035 and RFC 1123.
//
// Supported formats:
//   - Standard domains: "example.com", "subdomain.example.com"
//   - Wildcard domains: "*" or "*.example.com" (wildcard must be leftmost label)
//   - IDN (internationalized domains)
//   - Trailing dot optional: "example.com."
//
// Not allowed:
//   - Root domain: "."
//   - Empty labels: "example..com"
//   - Underscores in labels (except for SRV records, not supported here)
//   - Labels > 63 characters
//   - Total length > 253 characters
func IsValidDomain(domain string) (bool, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return false, errors.New("domain cannot be empty")
	}
	if domain == "." {
		return false, errors.New("root domain '.' is not allowed")
	}

	// Strip trailing dot if present
	if strings.HasSuffix(domain, ".") {
		domain = strings.TrimSuffix(domain, ".")
		if domain == "" {
			return false, errors.New("domain cannot be only a trailing dot")
		}
	}

	// Handle wildcard domains
	if domain == "*" {
		return true, nil
	}
	if strings.HasPrefix(domain, "*.") {
		rest := domain[2:]
		if rest == "" {
			return false, errors.New("'*.' must be followed by a domain")
		}
		return validateDomainLabels(rest)
	}

	return validateDomainLabels(domain)
}

// validateDomainLabels performs detailed validation of domain labels
func validateDomainLabels(domain string) (bool, error) {
	// Convert IDN to ASCII (Punycode)
	ascii, err := idna.Lookup.ToASCII(domain)
	if err != nil {
		return false, fmt.Errorf("invalid internationalized domain: %w", err)
	}

	// Check total length
	if len(ascii) > maxDomainLength {
		return false, fmt.Errorf("domain exceeds maximum length of %d characters", maxDomainLength)
	}

	// Check for invalid dot patterns
	if strings.Contains(ascii, "..") {
		return false, errors.New("domain contains empty labels (consecutive dots)")
	}
	if strings.HasPrefix(ascii, ".") || strings.HasSuffix(ascii, ".") {
		return false, errors.New("domain cannot start or end with a dot")
	}

	// Validate each label
	labels := strings.Split(ascii, ".")
	for i, label := range labels {
		if len(label) == 0 {
			return false, errors.New("domain contains empty label")
		}
		if len(label) > maxLabelLength {
			return false, fmt.Errorf("label '%s' exceeds maximum length of %d characters", label, maxLabelLength)
		}
		if strings.ContainsRune(label, '_') {
			return false, fmt.Errorf("label '%s' contains underscore (not allowed in hostnames)", label)
		}
		if !domainLabelRegex.MatchString(label) {
			return false, fmt.Errorf("label '%s' is invalid (must be alphanumeric with optional hyphens, no leading/trailing hyphen)", label)
		}

		// Additional check: TLD (last label) should not be all numeric
		if i == len(labels)-1 && IsNumeric(label) {
			return false, fmt.Errorf("top-level domain '%s' cannot be all numeric", label)
		}
	}
	return true, nil
}

// IsNumeric checks if a string contains only digits
func IsNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ==================
// Network Utilities
// ==================

// IsValidIPV4 validates an IP address
func IsValidIPV4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

func IsValidIPV6(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() == nil
}

// ParseCIDR splits the ip address in ip and cidr part
func ParseCIDR(cidr string) (string, int) {
	for i := len(cidr) - 1; i >= 0; i-- {
		if cidr[i] == '/' {
			return cidr[:i], atoi(cidr[i+1:])
		}
	}
	if containsColon(cidr) {
		return cidr, 128
	}
	return cidr, 32
}

// ====================
// Protobuf Utilities
// ====================

// MustMarshalAny marshals a protobuf message to anypb.Any
// In case of error, it panics.
// Panic makes sense here because the error should never happen.
// That can happen only due to programming errors.
func MustMarshalAny(pb interface{}) *anypb.Any {
	a, err := anypb.New(pb.(proto.Message))
	if err != nil {
		panic(err)
	}
	return a
}

// ================
// General Utilities
// ================

// UniqueSortedStrings returns a sorted slice with duplicates removed
func UniqueSortedStrings(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}

// atoi converts string to integer
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// containsColon checks if the given string contains at least one colon character ':' and returns true if found.
func containsColon(s string) bool {
	for _, c := range s {
		if c == ':' {
			return true
		}
	}
	return false
}
