package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Initialize manager
	manager, err := NewManager()
	if err != nil {
		log.Fatalf("Failed to initialize manager: %v", err)
	}
	defer manager.GracefulStop()

	// Generate initial snapshot
	if err = manager.ADS.generateSnapshot(manager.DB); err != nil {
		log.Fatalf("Failed to generate initial snapshot: %v", err)
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start manager services
	manager.Start()
	log.Println("Manager started successfully")

	// Wait for the termination signal
	sig := <-sigChan
	fmt.Printf("\nReceived signal %v, initiating graceful shutdown...\n", sig)

	// Cleanup and exit
	manager.GracefulStop()
	log.Println("Shutdown complete")
	os.Exit(0)
}
