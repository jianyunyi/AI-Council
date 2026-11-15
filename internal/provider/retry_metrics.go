package provider

import "context"

type retryCallbackKey struct{}

func WithRetryCallback(ctx context.Context, callback func()) context.Context {
	return context.WithValue(ctx, retryCallbackKey{}, callback)
}

func RecordRetry(ctx context.Context) {
	if callback, ok := ctx.Value(retryCallbackKey{}).(func()); ok && callback != nil {
		callback()
	}
}
