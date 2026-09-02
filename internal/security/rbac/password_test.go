package rbac

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/argon2"
)

func TestVerifyPasswordRejectsNonCanonicalPHC(t *testing.T) {
	encoded, err := HashPassword("password")
	require.NoError(t, err)
	newlineSalt := strings.Split(encoded, "$")
	newlineSalt[4] = newlineSalt[4][:1] + "\n" + newlineSalt[4][1:]

	tests := map[string]string{
		"version trailing garbage": strings.Replace(encoded, "v=19", "v=19garbage", 1),
		"params trailing garbage":  strings.Replace(encoded, "p=4$", "p=4garbage$", 1),
		"salt newline":             strings.Join(newlineSalt, "$"),
	}
	for name, malformed := range tests {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, VerifyPassword(malformed, "password"), ErrInvalidPassword)
		})
	}
}

func TestVerifyPasswordRejectsUnsafePHCParameters(t *testing.T) {
	tests := map[string]string{
		"memory":      makePasswordPHC(argonMemory+1, 1, 1, 16, 32),
		"iterations":  makePasswordPHC(8, 11, 1, 16, 32),
		"threads":     makePasswordPHC(8*17, 1, 17, 16, 32),
		"salt length": makePasswordPHC(8, 1, 1, 65, 32),
		"hash length": makePasswordPHC(8, 1, 1, 16, 65),
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, VerifyPassword(encoded, "password"), ErrInvalidPassword)
		})
	}
}

func makePasswordPHC(memory, iterations uint32, threads uint8, saltLen, hashLen int) string {
	salt := []byte(strings.Repeat("s", saltLen))
	hash := argon2.IDKey([]byte("password"), salt, iterations, memory, threads, uint32(hashLen))
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		memory,
		iterations,
		threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
}
