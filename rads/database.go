package main

import (
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"log"
	"time"
)

// Database connection pool configuration
const (
	readWriteMaxConnections = 1    // SQLite allows only one writer
	readOnlyMaxConnections  = 1000 // Allow many concurrent readers
	readOnlyIdleConnections = 100
	connMaxIdleTimeout      = 5 * time.Minute
	busyTimeoutMS           = 60000 // 60 seconds
)

func openSQLite(path string, rw bool) (*gorm.DB, func(), error) {
	// Use WAL for better concurrency and durability, enable foreign keys.
	// We set DSN flags and PRAGMAs again post-open to be explicit.
	dsn := fmt.Sprintf("%s?_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)

	if rw {
		dsn += fmt.Sprintf("&_busy_timeout=%d&_txlock=immediate", busyTimeoutMS)
	}

	// Configure logger
	logMode := logger.Silent
	if rw {
		logMode = logger.Info
	}

	// Open database
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:      logger.Default.LogMode(logMode),
		PrepareStmt: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool info
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	if rw {
		sqlDB.SetMaxOpenConns(readWriteMaxConnections)
		sqlDB.SetMaxIdleConns(readWriteMaxConnections)
	} else {
		sqlDB.SetMaxOpenConns(readOnlyMaxConnections)
		sqlDB.SetMaxIdleConns(readOnlyIdleConnections)
	}

	sqlDB.SetConnMaxIdleTime(connMaxIdleTimeout)

	// Return cleanup function
	closeDB := func() {
		mode := "ro"
		if rw {
			mode = "rw"
		}
		log.Printf("[DB] Closing %s connection: %s", mode, path)
		if err := sqlDB.Close(); err != nil {
			log.Printf("[DB] Error closing connection: %v", err)
		}
	}
	return db, closeDB, nil
}

// MigrateTables creates or updates the database schema for all models.
// This should be called once during application initialization.
func MigrateTables(db *gorm.DB) error {
	return db.AutoMigrate(
		&Message{},
		&TLSCertificate{},
		&Listener{},
		&Backend{},
		&IngressRule{},
		&HTTPRedirectRule{},
	)
}
