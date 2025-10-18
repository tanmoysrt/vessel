package main

import (
	"context"
	"errors"
	"fmt"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	discoveryGRPC "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
)

// ADSManager implements the Envoy xDS callback interface
// These callbacks are invoked by the envoy go-control-plane library
// when proxies connect and request configuration updates.
// Check https://www.envoyproxy.io/docs/envoy/latest/api-docs/xds_protocol for more details

// ============================================================================
// REST API Callbacks (Unimplemented - using streaming xDS only)
// ============================================================================

func (m *ADSManager) OnFetchRequest(_ context.Context, _ *discoveryGRPC.DiscoveryRequest) error {
	return errors.New("REST API not supported, use streaming xDS")
}

func (m *ADSManager) OnFetchResponse(_ *discoveryGRPC.DiscoveryRequest, _ *discoveryGRPC.DiscoveryResponse) {
	// No-op - REST API isn't implemented
}

// ============================================================================
// State of the World (SotW) Stream Callbacks
// ============================================================================

// OnStreamOpen is called when a proxy establishes a new xDS stream
func (m *ADSManager) OnStreamOpen(_ context.Context, streamID int64, typeURL string) error {
	if m.Config.Debug {
		fmt.Printf("[ADS] Stream opened: id=%d type=%s\n", streamID, typeURL)
	}
	return nil
}

// OnStreamClosed is called when a proxy closes its xDS stream
func (m *ADSManager) OnStreamClosed(streamID int64, node *corev3.Node) {
	if m.Config.Debug {
		fmt.Printf("[ADS] Stream closed: id=%d node=%s\n", streamID, node.GetId())
	}
	m.removeNodeFromInitializedNodes(node.GetId())
}

// OnStreamRequest handles incoming configuration requests from proxies
func (m *ADSManager) OnStreamRequest(streamID int64, request *discoveryGRPC.DiscoveryRequest) error {
	nodeID := request.GetNode().GetId()
	if m.Config.Debug {
		fmt.Printf("[ADS] Stream request: id=%d node=%s version=%s\n", streamID, nodeID, request.GetVersionInfo())
	}

	// Check if node already initialized (fast path with read lock)
	if m.isNodeInitialized(nodeID) {
		return nil
	}

	// Initialize the node with the latest snapshot
	return m.initializeNode(nodeID)
}
func (m *ADSManager) OnStreamResponse(_ context.Context, streamID int64, _ *discoveryGRPC.DiscoveryRequest, _ *discoveryGRPC.DiscoveryResponse) {
	if m.Config.Debug {
		fmt.Printf("[ADS] Stream response: id=%d\n", streamID)
	}
}

// ===========================
// Delta xDS Stream Callbacks
// ===========================

// OnDeltaStreamOpen is called when a proxy establishes a new delta xDS stream
func (m *ADSManager) OnDeltaStreamOpen(_ context.Context, streamID int64, typeURL string) error {
	if m.Config.Debug {
		fmt.Printf("[ADS] Delta stream opened: id=%d type=%s\n", streamID, typeURL)
	}
	return nil
}

// OnDeltaStreamClosed is called when a proxy closes its delta xDS stream
func (m *ADSManager) OnDeltaStreamClosed(streamID int64, node *corev3.Node) {
	if m.Config.Debug {
		fmt.Printf("[ADS] Delta stream closed: id=%d node=%s\n", streamID, node.GetId())
	}
	m.removeNodeFromInitializedNodes(node.GetId())
}

// OnStreamDeltaRequest handles incoming delta configuration requests
func (m *ADSManager) OnStreamDeltaRequest(streamID int64, request *discoveryGRPC.DeltaDiscoveryRequest) error {
	nodeID := request.GetNode().GetId()
	if m.Config.Debug {
		fmt.Printf("[ADS] Delta request: id=%d node=%s\n", streamID, nodeID)
	}

	// Check if node already initialized
	if m.isNodeInitialized(nodeID) {
		return nil
	}

	// Initialize the node with the latest snapshot
	return m.initializeNode(nodeID)
}

// OnStreamDeltaResponse is called after a delta response is sent
func (m *ADSManager) OnStreamDeltaResponse(streamID int64, _ *discoveryGRPC.DeltaDiscoveryRequest, _ *discoveryGRPC.DeltaDiscoveryResponse) {
	if m.Config.Debug {
		fmt.Printf("[ADS] Delta response: id=%d\n", streamID)
	}
}

// ===============
// Helper Methods
// ===============

// isNodeInitialized checks if a node has been initialized (thread-safe read)
func (m *ADSManager) isNodeInitialized(nodeID string) bool {
	m.InitializedNodesMutex.RLock()
	defer m.InitializedNodesMutex.RUnlock()
	val, ok := m.InitializedNodes[nodeID]
	return ok && val
}

// initializeNode sets the initial snapshot for a new proxy node
func (m *ADSManager) initializeNode(nodeID string) error {
	m.InitializedNodesMutex.Lock()
	defer m.InitializedNodesMutex.Unlock()

	// Double-check in case another goroutine initialized it
	val, ok := m.InitializedNodes[nodeID]
	isInitializedAlready := ok && val
	if isInitializedAlready {
		return nil
	}

	// Set the latest snapshot for this node
	cache := *m.CacheStore
	if err := cache.SetSnapshot(m.ctx, nodeID, m.LatestSnapshot); err != nil {
		return fmt.Errorf("failed to set snapshot for node %s: %w", nodeID, err)
	}

	m.InitializedNodes[nodeID] = true
	fmt.Printf("[ADS] Node initialized: %s\n", nodeID)
	return nil
}

func (m *ADSManager) removeNodeFromInitializedNodes(nodeID string) {
	m.InitializedNodesMutex.Lock()
	defer m.InitializedNodesMutex.Unlock()
	delete(m.InitializedNodes, nodeID)
	fmt.Printf("[ADS] Node removed from initialized nodes: %s\n", nodeID)
}
