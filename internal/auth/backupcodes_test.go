package auth

import "testing"

func TestGenerateBackupCodes(t *testing.T) {
	codes, err := GenerateBackupCodes()
	if err != nil {
		t.Fatalf("GenerateBackupCodes: %v", err)
	}
	if len(codes) != backupCodeCount {
		t.Fatalf("got %d codes, want %d", len(codes), backupCodeCount)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != backupCodeLength {
			t.Fatalf("code %q has length %d, want %d", c, len(c), backupCodeLength)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q", c)
		}
		seen[c] = true
	}
}

func TestNormalizeBackupCode(t *testing.T) {
	if got := NormalizeBackupCode(" abcd1234 "); got != "ABCD1234" {
		t.Fatalf("got %q, want ABCD1234", got)
	}
}
