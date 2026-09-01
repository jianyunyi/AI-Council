package tlsconfig

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReloaderRequiresCertificateFiles(t *testing.T) {
	_, err := New("missing-cert.pem", "missing-key.pem")
	require.Error(t, err)
}
