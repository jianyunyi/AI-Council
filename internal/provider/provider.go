package provider

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrUnauthorized  = errors.New("provider unauthorized")
	ErrRateLimited   = errors.New("provider rate limited")
	ErrInvalidOutput = errors.New("provider invalid output")
)

type APIError struct {
	Provider string
	Status   int
	Kind     error
	Message  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: status %d: %s", e.Provider, e.Status, e.Message)
}

func (e *APIError) Unwrap() error { return e.Kind }

type Message struct {
	Role    string
	Content string
}

type Usage struct {
	InputTokens         int64
	OutputTokens        int64
	EstimatedCostMicros int64
}

type Request struct {
	Model       string
	Messages    []Message
	JSONSchema  []byte
	Temperature float64
}

type Response struct {
	Content           []byte
	Usage             Usage
	ProviderRequestID string
}

type ModelProvider interface {
	Name() string
	Generate(context.Context, Request) (Response, error)
}
