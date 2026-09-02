package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "HTTP listen address")
	token := flag.String("token", "", "shutdown token")
	flag.Parse()

	shutdown := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/shutdown", func(w http.ResponseWriter, request *http.Request) {
		raw := strings.TrimSpace(request.Header.Get("Authorization"))
		if len(raw) < 7 || !strings.EqualFold(raw[:7], "bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(raw[7:])), []byte(*token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		select {
		case <-shutdown:
		default:
			close(shutdown)
		}
	})

	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: time.Second}
	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		os.Exit(1)
	}
}
