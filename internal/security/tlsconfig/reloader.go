package tlsconfig

import (
	"crypto/tls"
	"os"
	"sync"
)

type Reloader struct {
	certFile, keyFile   string
	mu                  sync.RWMutex
	cert                *tls.Certificate
	certMtime, keyMtime int64
}

func New(certFile, keyFile string) (*Reloader, error) {
	r := &Reloader{certFile: certFile, keyFile: keyFile}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}
func (r *Reloader) reload() error {
	cf, err := os.Stat(r.certFile)
	if err != nil {
		return err
	}
	kf, err := os.Stat(r.keyFile)
	if err != nil {
		return err
	}
	r.mu.RLock()
	same := r.cert != nil && r.certMtime == cf.ModTime().UnixNano() && r.keyMtime == kf.ModTime().UnixNano()
	r.mu.RUnlock()
	if same {
		return nil
	}
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.cert = &cert
	r.certMtime = cf.ModTime().UnixNano()
	r.keyMtime = kf.ModTime().UnixNano()
	r.mu.Unlock()
	return nil
}
func (r *Reloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if err := r.reload(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, nil
}
func (r *Reloader) Config() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: r.GetCertificate}
}
