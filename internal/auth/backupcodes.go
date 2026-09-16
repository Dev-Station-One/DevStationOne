package auth

import (
	"crypto/rand"
	"strings"
)

// backupCodeAlphabet excludes visually ambiguous characters (0/O, 1/I/L).
const backupCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

const (
	backupCodeLength = 8
	backupCodeCount  = 10
)

// GenerateBackupCodes returns 10 random 8-character single-use recovery
// codes, shown to the user once by the caller and stored only as hashes
// (HashToken).
func GenerateBackupCodes() ([]string, error) {
	codes := make([]string, backupCodeCount)
	for i := range codes {
		code, err := randomBackupCode()
		if err != nil {
			return nil, err
		}
		codes[i] = code
	}
	return codes, nil
}

// NormalizeBackupCode uppercases and trims user input so a code typed in
// lowercase still matches its stored hash.
func NormalizeBackupCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// GenerateEmailOTP returns a random 6-digit one-time code for the email-OTP
// second factor.
func GenerateEmailOTP() (string, error) {
	const digits = "0123456789"
	maxByte := 256 - (256 % len(digits))
	var sb strings.Builder
	buf := make([]byte, 1)
	for sb.Len() < 6 {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if int(buf[0]) >= maxByte {
			continue
		}
		sb.WriteByte(digits[int(buf[0])%len(digits)])
	}
	return sb.String(), nil
}

func randomBackupCode() (string, error) {
	// Rejection sampling avoids the modulo bias a plain `byte % len(alphabet)`
	// would introduce (256 is not a multiple of len(backupCodeAlphabet)).
	maxByte := 256 - (256 % len(backupCodeAlphabet))
	var sb strings.Builder
	buf := make([]byte, 1)
	for sb.Len() < backupCodeLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if int(buf[0]) >= maxByte {
			continue
		}
		sb.WriteByte(backupCodeAlphabet[int(buf[0])%len(backupCodeAlphabet)])
	}
	return sb.String(), nil
}
