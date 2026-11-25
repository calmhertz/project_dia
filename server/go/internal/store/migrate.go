package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func newMigrationProvider(databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	goose.SetBaseFS(migrationFiles)
	if err := goose.SetDialect("postgres"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set dialect: %w", err)
	}
	return db, nil
}

func MigrateUp(ctx context.Context, databaseURL string) error {
	db, err := newMigrationProvider(databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func MigrateDownTo(ctx context.Context, databaseURL string, version int64) error {
	db, err := newMigrationProvider(databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := goose.DownToContext(ctx, db, "migrations", version); err != nil {
		return fmt.Errorf("roll back migrations: %w", err)
	}
	return nil
}

func MigrationVersion(ctx context.Context, databaseURL string) (int64, error) {
	db, err := newMigrationProvider(databaseURL)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	version, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}
