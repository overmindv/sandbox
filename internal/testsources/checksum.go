package testsources

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func SHA256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func VerifySHA256(content []byte, expected string) error {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if expected == "" {
		return nil
	}
	actual := SHA256Hex(content)
	if actual != expected {
		return fmt.Errorf("%w: expected %s got %s", ErrChecksumMismatch, expected, actual)
	}
	return nil
}
