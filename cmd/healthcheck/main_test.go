package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeAcceptsOnlySuccessfulResponses(t *testing.T) {
	client := &http.Client{Timeout: time.Second}
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ok.Close()
	if err := probe(client, ok.URL); err != nil {
		t.Fatalf("probe successful response: %v", err)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	if err := probe(client, failing.URL); err == nil {
		t.Fatal("probe accepted an unavailable response")
	}
}
