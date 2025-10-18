package main

import (
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"log"
	"time"
)

func openSQLite(path string, rw bool) (*gorm.DB, func(), error) {
	// Use WAL for better concurrency and durability, enable foreign keys.
	// We set DSN flags and PRAGMAs again post-open to be explicit.
	dsn := fmt.Sprintf("%s?_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)

	if rw {
		dsn += "&_busy_timeout=60000&_txlock=immediate"
	}

	dbLogger := logger.Default.LogMode(logger.Silent)
	if rw {
		dbLogger = logger.Default.LogMode(logger.Info)
	}

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:      dbLogger,
		PrepareStmt: true,
	})
	if err != nil {
		return nil, nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, err
	}

	if rw {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		sqlDB.SetMaxOpenConns(1000)
		sqlDB.SetMaxIdleConns(100)
	}

	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	closeDB := func() {
		log.Printf("Closing SQLite connection (%s). Read Write : %s", path, rw)
		if err := sqlDB.Close(); err != nil {
			log.Printf("Error closing DB: %v", err)
		}
	}

	return db, closeDB, nil
}

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
