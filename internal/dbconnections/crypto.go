package dbconnections

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// secret is the plaintext JSON shape sealed into a database_connections row's
// encrypted_secret column (docs/SECURITY.md: "Encrypt/protect stored
// credentials").
type secret struct {
	Password    string `json:"password,omitempty"`
	ExtraParams string `json:"extra_params,omitempty"`
}

// sealSecret encrypts password/extraParams with AES-256-GCM under key
// (DEVSTATIONONE_DB_ENCRYPTION_KEY). The random nonce is prepended to the
// ciphertext, so no separate column is needed to store it.
func sealSecret(key []byte, password, extraParams string) ([]byte, error) {
	plaintext, err := json.Marshal(secret{Password: password, ExtraParams: extraParams})
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// openSecret decrypts a value produced by sealSecret.
func openSecret(key, ciphertext []byte) (secret, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return secret{}, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return secret{}, fmt.Errorf("gcm: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return secret{}, errors.New("ciphertext too short")
	}
	nonce, ct := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return secret{}, fmt.Errorf("decrypt: %w", err)
	}
	var s secret
	if err := json.Unmarshal(plaintext, &s); err != nil {
		return secret{}, err
	}
	return s, nil
}
