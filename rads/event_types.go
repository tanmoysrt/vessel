package main

import (
	"encoding/json"
	"time"
)

// ======================
// Event / Message Format
// ======================

// CommonEventParamsV1 consist of the parameters that are common to all events.
// This is metadata about the event and used to detect duplicate events.
type CommonEventParamsV1 struct {
	RequestID   string    `json:"request_id"`
	RequestedAt time.Time `json:"requested_at"`
}

// ResponsePayloadV1 is the format to send response back to the control plane
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

// TLSCertificateUpsertV1 is the event to upsert a TLS certificate.
// This event will be received in proxy.<agent_id>.request.v1.tls_certificate.upsert subject
type TLSCertificateUpsertV1 struct {
	CommonEventParamsV1
	IsWildcard bool   `json:"is_wildcard"`
	Domain     string `json:"domain"`
	Cert       string `json:"cert"`
	Key        string `json:"key"`
}

// TLSCertificateDeleteV1 is the event to delete a TLS certificate.
// This event will be received in proxy.<agent_id>.request.v1.tls_certificate.delete subject
type TLSCertificateDeleteV1 struct {
	CommonEventParamsV1
	IsWildcard bool   `json:"is_wildcard"`
	Domain     string `json:"domain"`
}

// IngressRuleUpsertV1 is the event to upsert an ingress rule.
// This event will be received in proxy.<agent_id>.request.v1.ingress_rule.upsert subject
type IngressRuleUpsertV1 struct {
	CommonEventParamsV1
	Priority           int                 `json:"priority"`
	BindIP             string              `json:"bind_ip"`
	Port               int                 `json:"port"`
	Protocol           ProtocolType        `json:"protocol"`
	IsTLS              bool                `json:"is_tls"`
	Domain             string              `json:"domain"`
	RoutePrefix        string              `json:"route_prefix"`
	AllowedCIDRs       []string            `json:"allowed_cidrs"`
	DeniedCIDRs        []string            `json:"denied_cidrs"`
	BackendResolver    BackendResolverType `json:"backend_resolver"`
	BackendDNSResolver string              `json:"backend_dns_resolver"`
	BackendHosts       []string            `json:"backend_hosts"` // For DNS Based Resolver, pass one value strictly
	BackendPort        int                 `json:"backend_port"`
	BackendIsTLS       bool                `json:"backend_is_tls"`
	BackendSNIDomain   string              `json:"backend_sni_domain"`
}

// IngressRuleDeleteV1 is the event to delete an ingress rule.
// This event will be received in proxy.<agent_id>.request.v1.ingress_rule.delete subject
type IngressRuleDeleteV1 struct {
	CommonEventParamsV1
	BindIP      string       `json:"bind_ip"`
	Port        int          `json:"port"`
	Protocol    ProtocolType `json:"protocol"`
	Domain      string       `json:"domain"`
	RoutePrefix string       `json:"route_prefix"`
}

// HTTPRedirectRuleUpsertV1 is the event to upsert an HTTP redirect rule.
// This event will be received in proxy.<agent_id>.request.v1.http_redirect_rule.upsert subject
type HTTPRedirectRuleUpsertV1 struct {
	CommonEventParamsV1
	Priority       int    `json:"priority"`
	BindIP         string `json:"bind_ip"`
	Port           int    `json:"port"`
	IsTLS          bool   `json:"is_tls"`
	Domain         string `json:"domain"` // Could be *
	RoutePrefix    string `json:"route_prefix"`
	SchemeRedirect string `json:"scheme_redirect"`
	HostRedirect   string `json:"host_redirect"`
	PathRedirect   string `json:"path_redirect"`
	StatusCode     int    `json:"status_code"`
}

// HTTPRedirectRuleDeleteV1 is the event to delete an HTTP redirect rule.
// This event will be received in proxy.<agent_id>.request.v1.http_redirect_rule.delete subject
type HTTPRedirectRuleDeleteV1 struct {
	CommonEventParamsV1
	BindIP         string `json:"bind_ip"`
	Port           int    `json:"port"`
	Domain         string `json:"domain"`
	RoutePrefix    string `json:"route_prefix"`
	SchemeRedirect string `json:"scheme_redirect"`
	HostRedirect   string `json:"host_redirect"`
	PathRedirect   string `json:"path_redirect"`
	StatusCode     int    `json:"status_code"`
}
