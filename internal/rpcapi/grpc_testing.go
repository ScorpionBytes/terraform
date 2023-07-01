package rpcapi

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

// grpcClientForTesting creates an in-memory-only gRPC server, offers the
// caller a chance to register services with it, and then returns a
// client connected to that fake server, with which the caller can construct
// service-specific client objects.
//
// When finished with the returned client, call the close callback given as
// the second return value or else you will leak some goroutines handling the
// server end of this fake connection.
func grpcClientForTesting(ctx context.Context, t *testing.T, registerServices func(srv *grpc.Server)) (conn grpc.ClientConnInterface, close func()) {
	fakeListener := bufconn.Listen(1024 /* buffer size */)
	srv := grpc.NewServer()

	// Caller gets an opportunity to register specific services before
	// we actually start "serving".
	registerServices(srv)

	go func() {
		if err := srv.Serve(fakeListener); err != nil {
			// We can't actually return an error here, but this should
			// not arise with our fake listener anyway so we'll just panic.
			panic(err)
		}
	}()

	fakeDialer := func(ctx context.Context, fakeAddr string) (net.Conn, error) {
		return fakeListener.DialContext(ctx)
	}
	realConn, err := grpc.DialContext(
		ctx, "testfake",
		grpc.WithContextDialer(fakeDialer),
		grpc.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("failed to connect to the fake server: %s", err)
	}

	return realConn, func() {
		realConn.Close()
		srv.Stop()
		fakeListener.Close()
	}
}
