package mail

import "testing"

func TestSend_Disabled(t *testing.T) {
	if err := Send(Config{}, "user@example.com", "subject", "body"); err == nil {
		t.Fatal("expected error when SMTP is not configured")
	}
}

func TestSend_RejectsHeaderInjection(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 25, From: "noreply@example.com"}
	if err := Send(cfg, "user@example.com\r\nBcc: evil@example.com", "subject", "body"); err == nil {
		t.Fatal("expected error for CRLF in To header")
	}
	if err := Send(cfg, "user@example.com", "subject\r\nBcc: evil@example.com", "body"); err == nil {
		t.Fatal("expected error for CRLF in Subject header")
	}
}
