package main

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	adsCache "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	adsServer "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"
	"sort"
	"sync"
)

// ==================
// Enums & Constants
// ===================

type ProtocolType string

const (
	HTTP ProtocolType = "http"
	TCP  ProtocolType = "tcp"
)

type BackendResolverType string

const (
	StaticResolver BackendResolverType = "static" // Static IP addresses
	DnsResolver    BackendResolverType = "dns"    // DNS-based resolution
)

type ProxyProtocolVersion int

const (
	ProxyProtocolNone ProxyProtocolVersion = iota // 0
	ProxyProtocolV1                               // 1
	ProxyProtocolV2                               // 2
)

func (p ProxyProtocolVersion) String() string {
	switch p {
	case ProxyProtocolNone:
		return "None"
	case ProxyProtocolV1:
		return "V1"
	case ProxyProtocolV2:
		return "V2"
	default:
		return "Unknown"
	}
}

// ===================
// Core Manager Types
// ===================

type Manager struct {
	Config *Config
	// Submanagers for specific parts
	DB   *DatabaseManager // Handles ReadOnly and ReadWrite DB manager
	NATS *NATSManager     // Manages NATS JetStream messaging
	ADS  *ADSManager      // Manages Envoy xDS (ADS) server

	wg        *sync.WaitGroup // Tracks all goroutines
	ctx       context.Context
	ctxCancel context.CancelFunc
}

// NATSManager manages NATS JetStream connections for event-driven communication
type NATSManager struct {
	Config               *NatsConfig
	IncomingStream       string
	IncomingStreamPrefix string
	OutgoingStream       string
	OutgoingStreamPrefix string
	MessageChan          chan *nats.Msg

	ctx context.Context
}

// DatabaseManager handles SQLite database connections with separate
// read-only and read-write connections.
type DatabaseManager struct {
	ReadOnly  *gorm.DB
	ReadWrite *gorm.DB

	ctx context.Context
}

// ADSManager manages the Envoy Aggregated Discovery Service (xDS) server.
// It generates configuration snapshots from the database state and distributes them.
// Then to connected Envoy proxy instances.
type ADSManager struct {
	Config                *ADSConfig
	LatestSnapshotVersion string             // Unix timestamp of the current snapshot
	LatestSnapshot        *adsCache.Snapshot // Current Envoy configuration
	CacheStore            *adsCache.SnapshotCache
	CacheOperationMutex   sync.Mutex        // To ensure only one operation is happening in CacheStore or LatestSnapshot variable
	Server                *adsServer.Server // Envoy xDS gRPC server instance
	InitializedNodes      map[string]bool   // Tracks which proxies received initial config
	InitializedNodesMutex sync.RWMutex      // Protects InitializedNodes map from concurrent access

	ctx context.Context
}

// ========================
// xDS Snapshot Generation
// ========================

// SnapshotGenerator converts database models into Envoy xDS API configuration
type SnapshotGenerator struct {
	version                       string
	numOfTrustedHops              int
	enableDownstreamProxyProtocol bool
	listeners                     []Listener
	backends                      []Backend
	ingressRules                  []IngressRule
	redirectRules                 []HTTPRedirectRule
	tlsCerts                      []TLSCertificate
}

// =====================
// Custom GORM DB Types
// =====================

// StringList is a GORM-compatible custom type that stores []string as JSON text in the DB.
type StringList []string

// Scan implements sql.Scanner.
// It reads JSON text from the DB and unmarshals into a []string.
func (s *StringList) Scan(value interface{}) error {
	if value == nil {
		*s = []string{}
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

// UnmarshalJSON ensures proper unmarshalling from JSON payloads (e.g., API input).
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
