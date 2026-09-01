package pairing

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPairingCodeExchangesOnceAndExpires(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := New(func() time.Time { return now }, bytes.NewReader(bytes.Repeat([]byte{1}, 128)))
	code, expiry, err := s.Start()
	require.NoError(t, err)
	require.Len(t, code, 6)
	require.Equal(t, now.Add(5*time.Minute), expiry)
	token, err := s.Exchange(code)
	require.NoError(t, err)
	require.Len(t, token, 43)
	_, err = s.Exchange(code)
	require.ErrorIs(t, err, ErrInvalidCode)
	now = now.Add(6 * time.Minute)
	_, err = s.Exchange(code)
	require.ErrorIs(t, err, ErrInvalidCode)
}
