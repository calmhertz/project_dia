// Command aagasa-rootpw resets the Root password from the server host.
//
// spec.md section 17.1 requires a server-side recovery mechanism for a
// compromise or lockout, usable when the UI is unavailable.
//
// The new password is read from the terminal without echo, or from stdin when
// piped, so it never appears in shell history, process arguments or logs.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"aagasa/internal/auth"
	"aagasa/internal/domain"
	"aagasa/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("AAGASA_POSTGRES_URL")
	if databaseURL == "" {
		return errors.New("AAGASA_POSTGRES_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	repo := store.NewRepository(pool)
	root, err := repo.GetUserByUsername(ctx, auth.BootstrapUsername)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("no root account exists; start the server once to bootstrap it")
		}
		return err
	}
	if root.Role != domain.RoleRoot {
		return fmt.Errorf("account %q is not the root account", auth.BootstrapUsername)
	}

	password, err := readPassword()
	if err != nil {
		return err
	}
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := repo.SetPassword(ctx, root.ID, hash); err != nil {
		return err
	}

	// Recovery is security-relevant, so it is auditable. The password is not
	// part of the record.
	if _, err := repo.AppendAudit(ctx, domain.AuditRecord{
		ActorUserID: &root.ID,
		Action:      "auth.root_password_reset_via_cli",
		EntityType:  "user",
		EntityID:    root.ID.String(),
		Reason:      "server-side recovery",
	}); err != nil {
		return fmt.Errorf("record audit entry: %w", err)
	}

	fmt.Println("root password updated")
	return nil
}

// readPassword prompts without echo on a terminal, and reads a single line
// when stdin is piped.
func readPassword() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "New root password: ")
		first, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}

		fmt.Fprint(os.Stderr, "Repeat password: ")
		second, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		if string(first) != string(second) {
			return "", errors.New("passwords do not match")
		}
		return string(first), nil
	}

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return "", errors.New("no password supplied on stdin")
	}
	return strings.TrimRight(scanner.Text(), "\r\n"), nil
}
