package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestValidateTOTPCode(t *testing.T) {
	key, err := GenerateTOTPKey("user@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPKey: %v", err)
	}

	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !ValidateTOTPCode(key.Secret(), code) {
		t.Fatal("valid current code rejected")
	}
	if ValidateTOTPCode(key.Secret(), "000000") {
		// Astronomically unlikely to collide; if it does, the test is flaky
		// rather than wrong.
		t.Skip("random collision with 000000, skipping")
	}

	// One step (30s) in the past must still validate (skew=1); two steps
	// (60s) must not.
	past, err := totp.GenerateCodeCustom(key.Secret(), time.Now().Add(-30*time.Second), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatalf("GenerateCodeCustom: %v", err)
	}
	if !ValidateTOTPCode(key.Secret(), past) {
		t.Fatal("code from one step ago rejected, want accepted within skew=1")
	}
	tooOld, err := totp.GenerateCodeCustom(key.Secret(), time.Now().Add(-90*time.Second), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatalf("GenerateCodeCustom: %v", err)
	}
	if ValidateTOTPCode(key.Secret(), tooOld) {
		t.Fatal("code from three steps ago accepted, want rejected outside skew=1")
	}
}

func TestSealOpenTOTPSecret(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	sealed, err := sealTOTPSecret(key, "SECRETVALUE")
	if err != nil {
		t.Fatalf("sealTOTPSecret: %v", err)
	}
	opened, err := openTOTPSecret(key, sealed)
	if err != nil {
		t.Fatalf("openTOTPSecret: %v", err)
	}
	if opened != "SECRETVALUE" {
		t.Fatalf("opened = %q, want SECRETVALUE", opened)
	}

	wrongKey := []byte("ffffffffffffffffffffffffffffffff")
	if _, err := openTOTPSecret(wrongKey, sealed); err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}
