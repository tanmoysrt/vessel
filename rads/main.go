package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	manager, err := NewManager()
	if err != nil {
		panic(err)
	}
	defer manager.Close()

	// Trap SIGINT/SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start actual tasks
	go manager.ListenToStream()
	go manager.StoreRequestsAndAcknowledge()
	go manager.ProcessRequests()
	go manager.SendResponsesToQueue()

	// Wait for signal
	sig := <-sigChan
	fmt.Printf("\nReceived signal: %v for graceful shutdown \n", sig)

	// Notify all other goroutines to exit
	manager.CancelContext()
	manager.Wg.Wait()

	// Call closure on manager
	manager.Close()

	fmt.Println("Graceful Shutdown Complete. Exiting.")
	os.Exit(0)
}
