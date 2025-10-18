package main

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"sort"
	"time"
)

type ProtocolType string

const (
	HTTP ProtocolType = "http"
	TCP  ProtocolType = "tcp"
)

type BackendResolverType string

const (
	STATIC_RESOLVER BackendResolverType = "static"
	DNS_RESOLVER    BackendResolverType = "dns"
)

type MessageInterface interface {
	Process(db *gorm.DB) (reply json.RawMessage, err error)
}

// Compile time checks -- to prevent shipping functions with invalid signature or unimplemented method
// Because, later we are going to use reflection to call the function based on the event type.
// So, we will assume that the function signature is correct.
var (
	_ MessageInterface = (*TLSCertificateUpsertV1)(nil)
	_ MessageInterface = (*TLSCertificateDeleteV1)(nil)
	_ MessageInterface = (*IngressRuleUpsertV1)(nil)
	_ MessageInterface = (*IngressRuleDeleteV1)(nil)
	_ MessageInterface = (*HTTPRedirectRuleUpsertV1)(nil)
	_ MessageInterface = (*HTTPRedirectRuleDeleteV1)(nil)
)

type CommonEventParamsV1 struct {
	RequestID   string    `json:"request_id"`
	RequestedAt time.Time `json:"requested_at"`
}

type ResponsePayloadV1 struct {
	CommonEventParamsV1
	MessageID    uint            `json:"-"` // Internal use only.
	Event        string          `json:"-"` // Internal use only.
	Success      bool            `json:"success"`
	Data         json.RawMessage `json:"data"`
	ErrorMessage string          `json:"error_message"`
	ProcessedAt  time.Time       `json:"processed_at"`
	QueuedAt     time.Time       `json:"queued_at"`
}

type TLSCertificateUpsertV1 struct {
	CommonEventParamsV1
	IsWildcard bool   `json:"is_wildcard"`
	Domain     string `json:"domain"`
	Cert       string `json:"cert"`
	Key        string `json:"key"`
}

type TLSCertificateDeleteV1 struct {
	CommonEventParamsV1
	Domain     string `json:"domain"`
	IsWildcard bool   `json:"is_wildcard"`
}

type IngressRuleUpsertV1 struct {
	CommonEventParamsV1

	Priority int `json:"priority"`

	BindIP   string       `json:"bind_ip"`
	Port     int          `json:"port"`
	Protocol ProtocolType `json:"protocol"`
	IsTLS    bool         `json:"is_tls"`

	Domain      string `json:"domain"`
	RoutePrefix string `json:"route_prefix"`

	AllowedCIDRs []string `json:"allowed_cidrs"`
	DeniedCIDRs  []string `json:"denied_cidrs"`

	BackendResolver    BackendResolverType `json:"backend_resolver"`
	BackendDNSResolver string              `json:"backend_dns_resolver"`
	BackendHosts       []string            `json:"backend_hosts"` // For DNS Based Resolver, pass one value strictly
	BackendPort        int                 `json:"backend_port"`

	BackendIsTLS     bool   `json:"backend_is_tls"`
	BackendSNIDomain string `json:"backend_sni_domain"`
}

type IngressRuleDeleteV1 struct {
	CommonEventParamsV1

	BindIP   string       `json:"bind_ip"`
	Port     int          `json:"port"`
	Protocol ProtocolType `json:"protocol"`

	Domain      string `json:"domain"`
	RoutePrefix string `json:"route_prefix"`
}

type HTTPRedirectRuleUpsertV1 struct {
	CommonEventParamsV1

	Priority int `json:"priority"`

	BindIP string `json:"bind_ip"`
	Port   int    `json:"port"`
	IsTLS  bool   `json:"is_tls"`

	Domain      string `json:"domain"` // Could be *
	RoutePrefix string `json:"route_prefix"`

	IsHttpsRedirect bool `json:"is_https_redirect"`

	SchemeRedirect string `json:"scheme_redirect"`
	HostRedirect   string `json:"host_redirect"`
	PathRedirect   string `json:"path_redirect"`
	StatusCode     int    `json:"status_code"`
}

type HTTPRedirectRuleDeleteV1 struct {
	CommonEventParamsV1

	BindIP string `json:"bind_ip"`
	Port   int    `json:"port"`

	Domain      string `json:"domain"`
	RoutePrefix string `json:"route_prefix"`

	IsHttpsRedirect bool `json:"is_https_redirect"`

	SchemeRedirect string `json:"scheme_redirect"`
	HostRedirect   string `json:"host_redirect"`
	PathRedirect   string `json:"path_redirect"`
	StatusCode     int    `json:"status_code"`
}

// StringList is a GORM-compatible custom type that stores []string as JSON text in the DB.
type StringList []string

// Scan implements sql.Scanner.
// It reads JSON text from the DB and unmarshals into a []string.
func (s *StringList) Scan(value interface{}) error {
	if value == nil {
		*s = []string{} // Changed from nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("failed to scan StringList: expected []byte or string, got %T", value)
	}

	var result []string
	if len(bytes) == 0 {
		*s = []string{}
		return nil
	}

	if err := json.Unmarshal(bytes, &result); err != nil {
		return fmt.Errorf("failed to unmarshal StringList: %w", err)
	}

	// Sort the string slice
	sort.Strings(result)
	*s = result
	return nil
}

// Value implements driver.Valuer.
// It marshals the []string into JSON text for storing in DB.
func (s StringList) Value() (driver.Value, error) {
	if len(s) == 0 {
		return "[]", nil
	}

	sorted := make([]string, len(s))
	copy(sorted, s)
	sort.Strings(sorted)

	bytes, err := json.Marshal(sorted)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal StringList: %w", err)
	}
	return string(bytes), nil
}

// MarshalJSON makes sure StringList serializes to JSON correctly (useful for API responses).
func (s StringList) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("[]"), nil
	}
	// Sort before marshaling
	sorted := make([]string, len(s))
	copy(sorted, s)
	sort.Strings(sorted)
	return json.Marshal(sorted)
}

// UnmarshalJSON ensures proper unmarshalling from JSON payloads (e.g. API input).
func (s *StringList) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = []string{}
		return nil
	}
	var result []string
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	// Sort after unmarshal
	sort.Strings(result)
	*s = result
	return nil
}
