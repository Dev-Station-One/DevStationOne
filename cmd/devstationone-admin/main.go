// Command devstationone-admin seeds or promotes an administrator account,
// replacing the DEVSTATIONONE_ADMIN_EMAIL/DEVSTATIONONE_ADMIN_PASSWORD
// startup env vars.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"devstationone/internal/auth"
	"devstationone/internal/config"
	"devstationone/internal/database"
)

const minPasswordLength = 8

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	email := flag.String("email", "", "administrator email (required)")
	flag.Parse()
	if *email == "" {
		return fmt.Errorf("--email is required")
	}

	password, err := readPassword(os.Stdin)
	if err != nil {
		return fmt.Errorf("read password from stdin: %w", err)
	}
	if len(password) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters", minPasswordLength)
	}

	cfg, err := config.LoadServer()
	if err != nil {
		return err
	}

	ctx := context.Background()
	db, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database connect failed: %w", err)
	}
	defer db.Close()

	u, created, err := auth.UpsertAdmin(ctx, db.Pool, *email, password)
	if err != nil {
		return fmt.Errorf("upsert admin: %w", err)
	}

	if created {
		fmt.Printf("created administrator %s\n", u.Email)
	} else {
		fmt.Printf("updated administrator %s\n", u.Email)
	}
	return nil
}

// readPassword reads a single line of password from r (piped via stdin,
// e.g. `echo "$PASSWORD" | devstationone-admin --email a@b.com`).
func readPassword(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("no password provided on stdin")
	}
	return strings.TrimSpace(scanner.Text()), nil
}
