// Command devstationone-admin seeds or promotes an administrator account
// (the default mode: --email + password on stdin), or mints a native-client
// device pairing code with QR (`pair --email ... --name ... --url ...`,
// ADR-053).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
	"devstationone/internal/config"
	"devstationone/internal/database"
	"devstationone/internal/devices"

	qrcode "github.com/skip2/go-qrcode"
)

const minPasswordLength = 8

func main() {
	if len(os.Args) > 1 && os.Args[1] == "pair" {
		f := flag.NewFlagSet("pair", flag.ExitOnError)
		email := f.String("email", "", "account the device pairs with (required)")
		name := f.String("name", "", "device name, e.g. \"Pixel\" (required)")
		serverURL := f.String("url", "", "base URL the phone reaches the Control Server at (required)")
		f.Parse(os.Args[2:])
		if err := runPair(*email, *name, *serverURL); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := runAdmin(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func openDB() (context.CancelFunc, *database.DB, error) {
	cfg, err := config.LoadServer()
	if err != nil {
		return nil, nil, err
	}
	ctx := context.Background()
	db, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("database connect failed: %w", err)
	}
	return db.Close, db, nil
}

func runAdmin() error {
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

	closeDB, db, err := openDB()
	if err != nil {
		return err
	}
	defer closeDB()

	u, created, err := auth.UpsertAdmin(context.Background(), db.Pool, *email, password)
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

// runPair mints a one-time pairing code for the named account and prints
// it with a QR the phone scans; the deep-link payload opens the app's
// pairing screen.
func runPair(email, name, serverURL string) error {
	if email == "" || name == "" || serverURL == "" {
		return fmt.Errorf("--email, --name and --url are all required")
	}
	u, err := url.Parse(serverURL)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return fmt.Errorf("--url must be an http(s) base URL, e.g. https://ds1.example.com")
	}

	closeDB, db, err := openDB()
	if err != nil {
		return err
	}
	defer closeDB()

	ctx := context.Background()
	user, err := auth.GetUserByEmail(ctx, db.Pool, email)
	if err != nil {
		return fmt.Errorf("user %s: %w", email, err)
	}

	code, err := devices.CreatePairingCode(ctx, db.Pool, user.ID, name)
	if err != nil {
		return fmt.Errorf("create pairing code: %w", err)
	}
	audit.Record(ctx, db.Pool, audit.EventDevicePairingCodeCreated, audit.Actor{UserID: user.ID, Email: user.Email}, "device", "", map[string]any{"name": name}, "cli")

	payload := fmt.Sprintf("devstation://pair?url=%s&code=%s", u, code)
	qr, err := newQR(payload)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(qr)
	fmt.Printf("URL:  %s\n", serverURL)
	fmt.Printf("Code: %s\n", code)
	fmt.Println("Single use, expires in 10 minutes, rate limit 10 per 15 min per IP.")
	return nil
}

// newQR renders the payload as a scannable terminal QR.
func newQR(payload string) (string, error) {
	q, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("render QR: %w", err)
	}
	return q.ToString(false), nil
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
