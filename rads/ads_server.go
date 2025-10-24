package main

import (
	"fmt"
	discoveryGRPC "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"net"
	"strings"
	"sync"
)

const grpcMaxConcurrentStreams = 1_000_000

// RunServer starts the gRPC server for Envoy's xDS API
func (m *ADSManager) RunServer(wg *sync.WaitGroup) {
	wg.Add(1)
	defer wg.Done()

	// Configure gRPC server options
	grpcOptions := []grpc.ServerOption{
		grpc.StreamInterceptor(m.gRPCServerAuthMiddleware()),
		grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams),
	}
	grpcServer := grpc.NewServer(grpcOptions...)

	// Create TCP listener
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", m.Config.BindPort))
	if err != nil {
		fmt.Printf("[ADS] Failed to create listener: %v\n", err)
		return
	}

	// Register ADS service
	discoveryGRPC.RegisterAggregatedDiscoveryServiceServer(grpcServer, *m.Server)
	fmt.Printf("[ADS] Server starting on port %d\n", m.Config.BindPort)

	// Start serving in the background
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			fmt.Printf("[ADS] Server error: %v\n", err)
		}
	}()

	// Wait for a shutdown signal
	<-m.ctx.Done()
	fmt.Println("[ADS] Shutting down server...")
	grpcServer.GracefulStop()
}

// gRPCServerAuthMiddleware is a gRPC server middleware that performs authentication using a bearer token.
// It checks for a valid "authorization" metadata header and verifies the token against the configured auth token.
func (m *ADSManager) gRPCServerAuthMiddleware() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Error(codes.Unauthenticated, "missing metadata")
		}

		vals := md.Get("authorization")
		if len(vals) == 0 {
			return status.Error(codes.Unauthenticated, "missing authorization header")
		}

		authHeader := vals[0]
		if !strings.HasPrefix(authHeader, "Bearer ") {
			return status.Error(codes.Unauthenticated, "invalid auth header format")
		}

		authToken := authHeader[7:]
		if authToken != m.Config.AuthToken {
			return status.Error(codes.PermissionDenied, "invalid bearer token")
		}

		return handler(srv, ss)
	}
}

// generateAndBroadcastADSChanges creates a new snapshot and broadcasts to all connected proxies
func (m *ADSManager) generateAndBroadcastADSChanges(dbManager *DatabaseManager) {
	fmt.Println("[ADS] Generating snapshot...")

	// Generate new snapshot from database state
	if err := m.generateSnapshot(dbManager); err != nil {
		fmt.Printf("[ADS] Failed to generate snapshot: %v\n", err)
		return
	}

	// Broadcast to all connected nodes
	m.broadcastSnapshotToNodes()

	//	Broadcast the snapshot
	m.CacheOperationMutex.Lock()
	defer m.CacheOperationMutex.Unlock()

	cache := *m.CacheStore
	nodes := cache.GetStatusKeys()

	for _, nodeID := range nodes {
		err := cache.SetSnapshot(m.ctx, nodeID, m.LatestSnapshot)
		if err != nil {
			fmt.Printf("Failed to broadcast snapshot to node: %v\n", err)
		}
		fmt.Printf("Broadcasted snapshot to node: %s\n", nodeID)
	}
}

// generateSnapshot generates a new snapshot from the database state and updates the latest snapshot info
func (m *ADSManager) generateSnapshot(dbManager *DatabaseManager) error {
	m.CacheOperationMutex.Lock()
	defer m.CacheOperationMutex.Unlock()

	var listeners []Listener
	var backends []Backend
	var ingressRules []IngressRule
	var httpRedirectRules []HTTPRedirectRule
	var tlsCertificates []TLSCertificate

	// Start a transaction to read all the data from the database.
	// To prevent any dirty reads, we use a read-only transaction.
	tx := dbManager.ReadOnly.Begin()
	defer tx.Rollback()

	err := tx.Find(&listeners).Error
	if err != nil {
		return err
	}

	err = tx.Find(&backends).Error
	if err != nil {
		return err
	}

	err = tx.Find(&ingressRules).Error
	if err != nil {
		return err
	}

	err = tx.Find(&httpRedirectRules).Error
	if err != nil {
		return err
	}

	err = tx.Find(&tlsCertificates).Error
	if err != nil {
		return err
	}

	// Release db lock
	tx.Rollback()

	// Generate Snapshot
	version, snapshot, err := GenerateSnapshot(m.Config.NumOfTrustedHops, m.Config.EnableDownstreamProxyProtocol, listeners, backends, ingressRules, httpRedirectRules, tlsCertificates)
	if err != nil {
		return err
	}

	// Update snapshot info
	// It only set the snapshot to manager, the snapshot is not sent to the nodes.
	m.LatestSnapshotVersion = version
	m.LatestSnapshot = snapshot

	return nil
}

// broadcastSnapshotToNodes sends the latest snapshot to all subscribed proxy nodes
func (m *ADSManager) broadcastSnapshotToNodes() {
	m.CacheOperationMutex.Lock()
	defer m.CacheOperationMutex.Unlock()

	cache := *m.CacheStore
	nodes := cache.GetStatusKeys()

	if len(nodes) == 0 {
		fmt.Println("[ADS] No connected nodes to broadcast to")
		return
	}

	// Broadcast to each node
	successCount := 0
	for _, nodeID := range nodes {
		if err := cache.SetSnapshot(m.ctx, nodeID, m.LatestSnapshot); err != nil {
			fmt.Printf("[ADS] Failed to broadcast to node %s: %v\n", nodeID, err)
		} else {
			successCount++
		}
	}

	fmt.Printf("[ADS] Broadcast complete: %d/%d nodes updated\n", successCount, len(nodes))
}
