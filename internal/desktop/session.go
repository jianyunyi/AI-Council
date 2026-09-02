package desktop

import (
	"crypto/rand"
	"encoding/base64"
)

const sessionTokenBytes = 32

func NewSessionToken() (string, error) {
	random := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}
