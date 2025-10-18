package main

import (
	"time"
)

type Message struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	Event           string     `gorm:"column:event" json:"event"` // Event == Stripped Prefix from NATS Stream Subject
	RequestID       string     `gorm:"column:request_id" json:"request_id"`
	RequestPayload  string     `gorm:"column:request_payload" json:"request_payload"`
	ResponsePayload string     `gorm:"column:response_payload" json:"response_payload"`
	ErrorMessage    string     `gorm:"column:error_message" json:"error_message"`
	Success         bool       `gorm:"column:success" json:"success"`
	Processed       bool       `gorm:"column:processed" json:"processed"`
	Replied         bool       `gorm:"column:replied" json:"replied"`
	RequestedAt     *time.Time `gorm:"column:requested_at" json:"requested_at"` // It's set by client only
	QueuedAt        *time.Time `gorm:"column:queued_at" json:"queued_at"`       // Time when this message is inserted in db
	ProcessedAt     *time.Time `gorm:"column:processed_at" json:"processed_at"` // When we have prepared a response
}

type TLSCertificate struct {
	ID         string    `gorm:"column:id;primaryKey" json:"id"`
	Domain     string    `gorm:"column:domain;index" json:"domain"`
	IsWildcard bool      `gorm:"column:is_wildcard;index;default:false" json:"is_wildcard"`
	Cert       string    `gorm:"column:cert" json:"cert"`
	Key        string    `gorm:"column:key" json:"-"`
	ExpiresAt  time.Time `gorm:"column:expires_at" json:"expires_at"`
}

type Listener struct {
	ID       string       `gorm:"primaryKey" json:"id"`
	Protocol ProtocolType `gorm:"column:protocol;index;default:http" json:"protocol"`
	IP       string       `gorm:"column:ip;index;default:0.0.0.0" json:"ip"`
	Port     int          `gorm:"column:port;index;default:80" json:"port"`
	IsTLS    bool         `gorm:"column:is_tls;index;default:false" json:"is_tls"`
}

type Backend struct {
	ID           string              `gorm:"primaryKey" json:"id"`
	ResolverType BackendResolverType `gorm:"column:resolver_type;index;default:static;not null" json:"resolver_type"`
	DNSResolver  string              `gorm:"column:dns_resolver" json:"dns_resolver,omitempty"` // e.g., "8.8.8.8:53"

	// Hosts as JSON string (e.g., '["10.0.0.1","10.0.0.2"]'
	Hosts StringList `gorm:"column:hosts;type:text;not null" json:"hosts"`
	Port  int        `gorm:"column:port;index;not null" json:"port"`

	// Upstream TLS
	IsTLS     bool   `gorm:"column:is_tls;index;default:false" json:"is_tls"`
	SNIDomain string `gorm:"column:sni_domain" json:"sni_domain,omitempty"`
}

type IngressRule struct {
	ID       string `gorm:"primaryKey" json:"id"`
	Priority int    `gorm:"column:priority;index;default:0;not null" json:"priority"`

	// Relation
	ListenerID string   `gorm:"column:listener_id;index;not null" json:"listener_id"`
	BackendID  string   `gorm:"column:backend_id;index;not null" json:"backend_id"`
	Listener   Listener `gorm:"foreignKey:ListenerID;references:ID" json:"listener"`
	Backend    Backend  `gorm:"foreignKey:BackendID;references:ID" json:"backend"`

	// Routing
	Domain      string `gorm:"column:domain;index" json:"domain,omitempty"`       // Empty means match all
	RoutePrefix string `gorm:"column:route_prefix;default:/" json:"route_prefix"` // Path prefix match

	// IP Allowlist / Blocklist - JSON array of CIDR blocks like ["10.0.0.0/8", "192.168.1.0/24"]
	AllowedCIDRs StringList `gorm:"column:allowed_cidrs;type:text" json:"allowed_cidrs,omitempty"`
	DeniedCIDRs  StringList `gorm:"column:denied_cidrs;type:text" json:"denied_cidrs,omitempty"`
}

type HTTPRedirectRule struct {
	ID         string   `gorm:"primaryKey" json:"id"`
	Priority   int      `gorm:"column:priority;index;default:0" json:"priority"`
	ListenerID string   `gorm:"column:listener_id;index;not null" json:"listener_id"`
	Listener   Listener `gorm:"foreignKey:ListenerID;references:ID" json:"listener"`

	// Matching
	Domain     string `gorm:"column:domain;index" json:"domain,omitempty"`
	PathPrefix string `gorm:"column:path_prefix;default:/" json:"path_prefix"`

	// Flag to differentiate between HTTPS Redirect and Path Redirect
	IsHttpsRedirect bool `gorm:"column:is_https_redirect;index;default:false" json:"is_https_redirect"`

	// Redirect Target
	SchemeRedirect string `gorm:"column:scheme_redirect" json:"scheme_redirect,omitempty"` // https
	HostRedirect   string `gorm:"column:host_redirect" json:"host_redirect,omitempty"`
	PathRedirect   string `gorm:"column:path_redirect" json:"path_redirect,omitempty"`
	StatusCode     int    `gorm:"column:status_code;default:301" json:"status_code"` // 301, 302, 307, 308
}
