package dbconnections

import "testing"

func testKey() []byte { return []byte("0123456789abcdef0123456789abcdef") } // 32 bytes

func TestSealOpenSecret_RoundTrip(t *testing.T) {
	key := testKey()
	sealed, err := sealSecret(key, "hunter2", "sslrootcert=/etc/pki/root.crt")
	if err != nil {
		t.Fatalf("sealSecret: %v", err)
	}
	s, err := openSecret(key, sealed)
	if err != nil {
		t.Fatalf("openSecret: %v", err)
	}
	if s.Password != "hunter2" || s.ExtraParams != "sslrootcert=/etc/pki/root.crt" {
		t.Errorf("got %+v, want password=hunter2 extra_params=sslrootcert=/etc/pki/root.crt", s)
	}
}

func TestSealSecret_NondeterministicNonce(t *testing.T) {
	key := testKey()
	a, err := sealSecret(key, "hunter2", "")
	if err != nil {
		t.Fatalf("sealSecret: %v", err)
	}
	b, err := sealSecret(key, "hunter2", "")
	if err != nil {
		t.Fatalf("sealSecret: %v", err)
	}
	if string(a) == string(b) {
		t.Error("two seals of the same plaintext produced identical ciphertext — nonce isn't being randomized")
	}
}

func TestOpenSecret_WrongKeyFails(t *testing.T) {
	sealed, err := sealSecret(testKey(), "hunter2", "")
	if err != nil {
		t.Fatalf("sealSecret: %v", err)
	}
	wrongKey := []byte("fedcba9876543210fedcba9876543210"[:32])
	if _, err := openSecret(wrongKey, sealed); err == nil {
		t.Error("expected an error decrypting with the wrong key")
	}
}

func TestOpenSecret_TamperedCiphertextFails(t *testing.T) {
	key := testKey()
	sealed, err := sealSecret(key, "hunter2", "")
	if err != nil {
		t.Fatalf("sealSecret: %v", err)
	}
	sealed[len(sealed)-1] ^= 0xFF
	if _, err := openSecret(key, sealed); err == nil {
		t.Error("expected an error decrypting tampered ciphertext")
	}
}
