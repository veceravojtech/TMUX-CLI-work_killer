package redact

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Layer 1: built-in masker golden tests (one per catalogue class) ---

func TestMask_BearerAuthorizationHeader(t *testing.T) {
	in := "curl -H 'Authorization: Bearer abc123.def-456_ghi' https://api.example.com"
	out := Mask(in)
	assert.Equal(t, "curl -H 'Authorization: Bearer «REDACTED:bearer»' https://api.example.com", out)
}

func TestMask_BearerHeaderCaseInsensitive(t *testing.T) {
	out := Mask("AUTHORIZATION: BEARER tok123456")
	assert.Equal(t, "AUTHORIZATION: BEARER «REDACTED:bearer»", out)
}

func TestMask_BareJWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	out := Mask("token dump: " + jwt + " end")
	assert.Equal(t, "token dump: «REDACTED:bearer» end", out)
}

func TestMask_APIKeyOpenAI(t *testing.T) {
	out := Mask("export OPENAI_KEY " + "sk-proj-abcdefghijklmnop1234")
	assert.Equal(t, "export OPENAI_KEY «REDACTED:apikey»", out)
}

func TestMask_APIKeyAWS(t *testing.T) {
	out := Mask("aws key AKIAIOSFODNN7EXAMPLE in use")
	assert.Equal(t, "aws key «REDACTED:apikey» in use", out)
}

func TestMask_APIKeyGoogle(t *testing.T) {
	out := Mask("gcp AIzaSyA1234567890abcdefghijklmnopqrstuv ok")
	assert.Equal(t, "gcp «REDACTED:apikey» ok", out)
}

func TestMask_APIKeyGitHub(t *testing.T) {
	for _, tok := range []string{
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"gho_abcdefghijklmnopqrstuvwxyz0123456789",
		"ghu_abcdefghijklmnopqrstuvwxyz0123456789",
		"ghs_abcdefghijklmnopqrstuvwxyz0123456789",
		"ghr_abcdefghijklmnopqrstuvwxyz0123456789",
	} {
		assert.Equal(t, "push with «REDACTED:apikey» done", Mask("push with "+tok+" done"), tok)
	}
}

func TestMask_PrivateKeyBlock(t *testing.T) {
	in := "before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEow...\nmore\n-----END RSA PRIVATE KEY-----\nafter"
	assert.Equal(t, "before\n«REDACTED:privatekey»\nafter", Mask(in))
}

func TestMask_PrivateKeyBlockGeneric(t *testing.T) {
	in := "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----"
	assert.Equal(t, "«REDACTED:privatekey»", Mask(in))
}

func TestMask_KVEquals(t *testing.T) {
	assert.Equal(t, "password=«REDACTED:kv»", Mask("password=hunter2"))
	assert.Equal(t, "export API_KEY=«REDACTED:kv»", Mask("export API_KEY=abc123"))
	assert.Equal(t, "access-key=«REDACTED:kv»", Mask("access-key=xyz"))
}

func TestMask_KVColon(t *testing.T) {
	assert.Equal(t, "secret: «REDACTED:kv»", Mask("secret: s3cr3t"))
	assert.Equal(t, "passwd:«REDACTED:kv»", Mask("passwd:pw"))
}

func TestMask_KVJSONQuoted(t *testing.T) {
	assert.Equal(t, `{"password": "«REDACTED:kv»"}`, Mask(`{"password": "hunter2"}`))
	assert.Equal(t, `{"api_key":"«REDACTED:kv»"}`, Mask(`{"api_key":"abc"}`))
}

func TestMask_KVCaseInsensitive(t *testing.T) {
	assert.Equal(t, "PASSWORD=«REDACTED:kv»", Mask("PASSWORD=abc"))
}

func TestMask_KVMasksValueOnly(t *testing.T) {
	out := Mask("db password=verysecret rest of line")
	assert.Equal(t, "db password=«REDACTED:kv» rest of line", out)
}

func TestMask_URLCreds(t *testing.T) {
	out := Mask("dsn postgres://admin:s3cr3t@db.internal:5432/app")
	assert.Equal(t, "dsn postgres://«REDACTED:urlcreds»@db.internal:5432/app", out)
}

func TestMask_PlainTextUntouched(t *testing.T) {
	in := "ordinary build output: compiled 12 packages in 3.4s (no secrets here)"
	assert.Equal(t, in, Mask(in))
}

func TestMask_URLWithoutCredsUntouched(t *testing.T) {
	in := "fetching https://example.com/path?q=1"
	assert.Equal(t, in, Mask(in))
}

// --- Layer 2: optional user hook (fail-closed) ---

func writeHook(t *testing.T, dir, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, "redact-transcript.sh")
	require.NoError(t, os.WriteFile(p, []byte(body), mode))
	return p
}

func TestHook_AbsentIsNotRunnable(t *testing.T) {
	h := Hook{Path: filepath.Join(t.TempDir(), "nope.sh")}
	assert.False(t, h.Runnable())
}

func TestHook_NonExecutableIsNotRunnable(t *testing.T) {
	p := writeHook(t, t.TempDir(), "#!/bin/sh\ncat\n", 0o644)
	assert.False(t, Hook{Path: p}.Runnable())
}

func TestHook_ExecutableIsRunnable(t *testing.T) {
	p := writeHook(t, t.TempDir(), "#!/bin/sh\ncat\n", 0o755)
	assert.True(t, Hook{Path: p}.Runnable())
}

func TestHook_RunTransformsStdinToStdout(t *testing.T) {
	p := writeHook(t, t.TempDir(), "#!/bin/sh\nsed 's/internal-name/«REDACTED:custom»/g'\n", 0o755)
	out, err := Hook{Path: p}.Run(context.Background(), "call internal-name now\n")
	require.NoError(t, err)
	assert.Equal(t, "call «REDACTED:custom» now\n", out)
}

func TestHook_RunNonZeroExitIsError(t *testing.T) {
	p := writeHook(t, t.TempDir(), "#!/bin/sh\nexit 3\n", 0o755)
	_, err := Hook{Path: p}.Run(context.Background(), "text\n")
	require.Error(t, err)
}

func TestHook_RunTimeoutIsError(t *testing.T) {
	p := writeHook(t, t.TempDir(), "#!/bin/sh\nsleep 5\n", 0o755)
	h := Hook{Path: p, Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err := h.Run(context.Background(), "text\n")
	require.Error(t, err)
	assert.Less(t, time.Since(start), 3*time.Second, "timeout must fire well before the sleep completes")
}
