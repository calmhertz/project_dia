package workerapi

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func callWith(t *testing.T, md metadata.MD) error {
	t.Helper()
	interceptor := SharedSecretInterceptor(testSecret)
	ctx := context.Background()
	if md != nil {
		ctx = metadata.NewIncomingContext(ctx, md)
	}
	handler := func(context.Context, any) (any, error) { return "ok", nil }
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, handler)
	return err
}

func TestInterceptorAcceptsCorrectSecret(t *testing.T) {
	if err := callWith(t, metadata.Pairs(authorizationHeader, bearerPrefix+testSecret)); err != nil {
		t.Fatalf("expected call to be accepted, got %v", err)
	}
}

// Every rejection path must be Unauthenticated so an unauthenticated Worker can
// never reach a handler.
func TestInterceptorRejectsBadCredentials(t *testing.T) {
	cases := map[string]metadata.MD{
		"no metadata":      nil,
		"missing header":   metadata.Pairs("other", "value"),
		"empty value":      metadata.Pairs(authorizationHeader, ""),
		"wrong secret":     metadata.Pairs(authorizationHeader, bearerPrefix+"wrong-secret-value-that-is-long"),
		"missing prefix":   metadata.Pairs(authorizationHeader, "wrong"),
		"secret prefix":    metadata.Pairs(authorizationHeader, bearerPrefix+testSecret[:16]),
		"duplicate header": metadata.MD{authorizationHeader: []string{bearerPrefix + testSecret, bearerPrefix + testSecret}},
	}

	for name, md := range cases {
		t.Run(name, func(t *testing.T) {
			err := callWith(t, md)
			if err == nil {
				t.Fatal("expected call to be rejected")
			}
			if status.Code(err) != codes.Unauthenticated {
				t.Errorf("code = %v, want Unauthenticated", status.Code(err))
			}
		})
	}
}
