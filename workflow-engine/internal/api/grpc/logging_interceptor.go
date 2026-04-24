package grpc

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// grpcLogSkipMethods lists full RPC method names whose successful calls are
// suppressed from the access log. Workers call PollJobs in a tight loop —
// logging every hit drowns out real traffic.
var grpcLogSkipMethods = map[string]struct{}{
	"/workflow.v1.WorkflowEngine/PollJobs": {},
}

// LoggingUnaryInterceptor emits one structured log line per unary RPC with
// method, resulting status code, duration, and error text (if any). Attached
// once at server construction — handlers never need to log manually.
//
// Methods listed in grpcLogSkipMethods are silenced on success but still log
// on error, so real failures on the poll endpoint remain visible.
func LoggingUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)

		if _, skip := grpcLogSkipMethods[info.FullMethod]; skip && err == nil {
			return resp, err
		}

		code := status.Code(err).String()
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelWarn
		}
		attrs := []slog.Attr{
			slog.String("method", info.FullMethod),
			slog.String("code", code),
			slog.Duration("duration", time.Since(start)),
		}
		if err != nil {
			attrs = append(attrs, slog.String("err", err.Error()))
		}
		slog.LogAttrs(ctx, level, "grpc request", attrs...)
		return resp, err
	}
}
