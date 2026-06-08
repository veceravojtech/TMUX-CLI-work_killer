package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFingerprint_Deterministic(t *testing.T) {
	first := Fingerprint()
	second := Fingerprint()
	require.Equal(t, first, second)
	assert.Len(t, first, 64)
}

func TestFingerprint_LowercaseHex(t *testing.T) {
	assert.Regexp(t, "^[0-9a-f]{64}$", Fingerprint())
}

func TestHashString_Known(t *testing.T) {
	// SHA256 of the empty string.
	const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	assert.Equal(t, emptyHash, hashString(""))
}
