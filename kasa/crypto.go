package kasa

import (
	"bytes"
	"encoding/binary"
)

// Encrypt applies the Kasa XOR stream cipher to the string and returns the
// encrypted bytes (no length header). Use EncryptWithHeader for TCP frames.
func Encrypt(s string) []byte {
	key := byte(171)
	payload := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		payload[i] = s[i] ^ key
		key = payload[i]
	}
	return payload
}

// Decrypt reverses the Kasa XOR stream cipher, returning the plaintext string.
func Decrypt(b []byte) string {
	key := byte(171)
	result := make([]byte, len(b))
	for i := 0; i < len(b); i++ {
		result[i] = b[i] ^ key
		key = b[i]
	}
	return string(result)
}

// EncryptWithHeader wraps the payload in the TCP header (4 bytes length)
func EncryptWithHeader(s string) []byte {
	payload := Encrypt(s)
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, uint32(len(payload)))
	buf.Write(payload)
	return buf.Bytes()
}
