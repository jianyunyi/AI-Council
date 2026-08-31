package pairing

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"strings"
	"sync"
	"time"
)

var ErrInvalidCode = errors.New("invalid or expired pairing code")

type Service struct {
	mu        sync.Mutex
	now       func() time.Time
	random    io.Reader
	codeHash  [32]byte
	expiresAt time.Time
}

func New(now func() time.Time, random io.Reader) *Service {
	if now == nil {
		now = time.Now
	}
	if random == nil {
		random = rand.Reader
	}
	return &Service{now: now, random: random}
}

func (s *Service) Start() (string, time.Time, error) {
	code, err := randomCode(s.random, 6)
	if err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codeHash = sha256.Sum256([]byte(code))
	s.expiresAt = s.now().Add(5 * time.Minute)
	return code, s.expiresAt, nil
}

func (s *Service) Exchange(code string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	provided := sha256.Sum256([]byte(code))
	if s.now().After(s.expiresAt) || subtle.ConstantTimeCompare(s.codeHash[:], provided[:]) != 1 {
		return "", ErrInvalidCode
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(s.random, raw); err != nil {
		return "", err
	}
	s.codeHash = [32]byte{}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func randomCode(r io.Reader, n int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var b strings.Builder
	for i := 0; i < n; i++ {
		x, err := rand.Int(r, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[x.Int64()])
	}
	return b.String(), nil
}
