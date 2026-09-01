package grpc

import (
	"context"
	"crypto/subtle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"strings"
)

func UnaryAuthInterceptor(expected string) grpc.ServerOption {
	return grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if expected == "" {
			return handler(ctx, req)
		}
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing authorization")
		}
		values := md.Get("authorization")
		if len(values) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization")
		}
		provided := strings.TrimSpace(values[0])
		if len(provided) < 7 || !strings.EqualFold(provided[:7], "bearer ") {
			return nil, status.Error(codes.Unauthenticated, "invalid authorization")
		}
		token := strings.TrimSpace(provided[7:])
		if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
			return nil, status.Error(codes.PermissionDenied, "invalid token")
		}
		return handler(ctx, req)
	})
}
