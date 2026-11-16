package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type responseRoundTripper int

func (status responseRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: int(status),
		Status:     http.StatusText(int(status)),
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func TestProbeAcceptsOnlySuccessfulResponses(t *testing.T) {
	client := &http.Client{Timeout: time.Second, Transport: responseRoundTripper(http.StatusNoContent)}
	if err := probe(client, "http://example.test/healthz"); err != nil {
		t.Fatalf("probe successful response: %v", err)
	}

	client.Transport = responseRoundTripper(http.StatusServiceUnavailable)
	if err := probe(client, "http://example.test/healthz"); err == nil {
		t.Fatal("probe accepted an unavailable response")
	}
}
