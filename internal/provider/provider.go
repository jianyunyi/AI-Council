package provider

import "context"

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
