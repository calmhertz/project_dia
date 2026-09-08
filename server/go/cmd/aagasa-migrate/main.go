// Command aagasa-migrate applies Aagasa's PostgreSQL schema migrations.
//
// Schema changes are a deliberate operational step, separate from running the
// Server (RULES.md section 18).
//
//	aagasa-migrate up
//	aagasa-migrate version
//	aagasa-migrate down-to <version>
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"aagasa/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	databaseURL := os.Getenv("AAGASA_POSTGRES_URL")
	if databaseURL == "" {
		return fmt.Errorf("AAGASA_POSTGRES_URL is not set")
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: aagasa-migrate up|version|mongo-init|down-to <version>")
	}

	ctx := context.Background()

	switch args[0] {
	case "up":
		if err := store.MigrateUp(ctx, databaseURL); err != nil {
			return err
		}
		version, err := store.MigrationVersion(ctx, databaseURL)
		if err != nil {
			return err
		}
		fmt.Printf("schema version %d\n", version)
		return nil

	case "version":
		version, err := store.MigrationVersion(ctx, databaseURL)
		if err != nil {
			return err
		}
		fmt.Printf("schema version %d\n", version)
		return nil

	case "mongo-init":
		mongoURL := os.Getenv("AAGASA_MONGO_URL")
		if mongoURL == "" {
			return fmt.Errorf("AAGASA_MONGO_URL is not set")
		}
		databaseName := os.Getenv("AAGASA_MONGO_DATABASE")
		if databaseName == "" {
			databaseName = "aagasa"
		}
		if err := store.EnsureMongoDatabase(ctx, mongoURL, databaseName); err != nil {
			return err
		}
		fmt.Printf("mongodb collections ready in %s\n", databaseName)
		return nil

	case "down-to":
		if len(args) != 2 {
			return fmt.Errorf("usage: aagasa-migrate down-to <version>")
		}
		version, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("version must be a number")
		}
		return store.MigrateDownTo(ctx, databaseURL, version)

	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}
