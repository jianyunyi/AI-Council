package rbac

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16

	argonMaxTime    = 10
	argonMaxMemory  = 64 * 1024
	argonMaxThreads = 16
	argonMaxSaltLen = 64
	argonMaxKeyLen  = 64
)

var ErrInvalidPassword = errors.New("invalid password")

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(encoded, password string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrInvalidPassword
	}
	version, ok := parsePHCUint(parts[2], "v=", 32)
	if !ok || version != argon2.Version {
		return ErrInvalidPassword
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return ErrInvalidPassword
	}
	memory, memoryOK := parsePHCUint(params[0], "m=", 32)
	iterations, iterationsOK := parsePHCUint(params[1], "t=", 32)
	threads, threadsOK := parsePHCUint(params[2], "p=", 8)
	if !memoryOK || memory == 0 || memory > argonMaxMemory ||
		!iterationsOK || iterations == 0 || iterations > argonMaxTime ||
		!threadsOK || threads == 0 || threads > argonMaxThreads {
		return ErrInvalidPassword
	}
	salt, ok := decodePHCBase64(parts[4], argonMaxSaltLen)
	if !ok {
		return ErrInvalidPassword
	}
	want, ok := decodePHCBase64(parts[5], argonMaxKeyLen)
	if !ok {
		return ErrInvalidPassword
	}
	got := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(threads), uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrInvalidPassword
	}
	return nil
}

func parsePHCUint(field, prefix string, bitSize int) (uint64, bool) {
	raw, ok := strings.CutPrefix(field, prefix)
	if !ok || raw == "" {
		return 0, false
	}
	value, err := strconv.ParseUint(raw, 10, bitSize)
	if err != nil || strconv.FormatUint(value, 10) != raw {
		return 0, false
	}
	return value, true
}

func decodePHCBase64(field string, maxDecodedLen int) ([]byte, bool) {
	if field == "" || len(field) > base64.RawStdEncoding.EncodedLen(maxDecodedLen) {
		return nil, false
	}
	decoded, err := base64.RawStdEncoding.DecodeString(field)
	if err != nil || len(decoded) == 0 || len(decoded) > maxDecodedLen ||
		base64.RawStdEncoding.EncodeToString(decoded) != field {
		return nil, false
	}
	return decoded, true
}
