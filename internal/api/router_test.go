package api

// testDBEncryptionKey is a 32-byte AES-256 key used by every NewRouter call
// in this package's tests — the value doesn't matter, only its length.
var testDBEncryptionKey = []byte("0123456789abcdef0123456789abcdef")
