package capproxy

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type testResolver struct {
	binding SessionBinding
}

type testBindingResolver struct{}

func (r testResolver) ResolveCapabilitySession(_ context.Context, token string) (SessionBinding, error) {
	if token != "session-token" {
		return SessionBinding{}, status.Error(codes.Unauthenticated, "bad token")
	}
	return r.binding, nil
}

func (testBindingResolver) ResolveCapabilityBinding(_ context.Context, _, _ string) (map[string]string, error) {
	return map[string]string{
		"x-octobus-service":  "svc",
		"x-octobus-instance": "inst",
	}, nil
}

func staticOctoBus(addr, token string) OctoBusResolver {
	return func(context.Context) (string, string, bool) {
		return addr, token, true
	}
}

// recordingBindingResolver flags whether the catalog fill path was invoked.
type recordingBindingResolver struct{ called bool }

func (r *recordingBindingResolver) ResolveCapabilityBinding(context.Context, string, string) (map[string]string, error) {
	r.called = true
	return map[string]string{"x-octobus-service": "from-catalog", "x-octobus-instance": "from-catalog"}, nil
}

func TestProxyInjectsOctoBusMetadata(t *testing.T) {
	var received metadata.MD
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error {
		received, _ = metadata.FromIncomingContext(stream.Context())
		req := rawFrame(nil)
		if err := stream.RecvMsg(&req); err != nil {
			return err
		}
		return stream.SendMsg(rawFrame("ok:" + string(req)))
	})
	defer stopOcto()
	proxyAddr, stopProxy := startTestProxy(t, Config{Listen: "127.0.0.1:0", OctoBus: staticOctoBus(octoAddr, "octo-token")}, testResolver{binding: SessionBinding{SessionID: "s1", CapsetIDs: []string{"dev"}}})
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(SessionTokenMetadata, "session-token"))
	out := rawFrame(nil)
	if err := conn.Invoke(ctx, "/pkg.Service/Call", rawFrame("ping"), &out); err != nil {
		t.Fatal(err)
	}
	if string(out) != "ok:ping" {
		t.Fatalf("unexpected response %q", string(out))
	}
	for key, want := range map[string]string{
		"x-octobus-capset":   "dev",
		"x-octobus-service":  "svc",
		"x-octobus-instance": "inst",
		"authorization":      "Bearer octo-token",
	} {
		if got := firstMetadata(received, key); got != want {
			t.Fatalf("metadata %s = %q, want %q", key, got, want)
		}
	}
}

func TestProxyForwardsGuestSuppliedRouting(t *testing.T) {
	var received metadata.MD
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error {
		received, _ = metadata.FromIncomingContext(stream.Context())
		req := rawFrame(nil)
		if err := stream.RecvMsg(&req); err != nil {
			return err
		}
		return stream.SendMsg(rawFrame("ok:" + string(req)))
	})
	defer stopOcto()
	bindings := &recordingBindingResolver{}
	proxyAddr, stopProxy := startTestProxyWithBindings(t, Config{Listen: "127.0.0.1:0", OctoBus: staticOctoBus(octoAddr, "")}, testResolver{binding: SessionBinding{SessionID: "s1", CapsetIDs: []string{"dev"}}}, bindings)
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Guest supplies an allowed capset plus its own service/instance (from the
	// injected guide); all three are forwarded and the catalog fill is skipped.
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.MD{
		SessionTokenMetadata: []string{"session-token"},
		"x-octobus-service":  []string{"guest-svc"},
		"x-octobus-instance": []string{"guest-inst"},
		"x-octobus-capset":   []string{"dev"},
	})
	out := rawFrame(nil)
	if err := conn.Invoke(ctx, "/pkg.Service/Call", rawFrame("ping"), &out); err != nil {
		t.Fatal(err)
	}
	if bindings.called {
		t.Fatalf("catalog fill must be skipped when guest supplies service/instance")
	}
	for key, want := range map[string]string{
		"x-octobus-capset":   "dev",        // guest-supplied, validated in the allowed set
		"x-octobus-service":  "guest-svc",  // guest value forwarded
		"x-octobus-instance": "guest-inst", // guest value forwarded
	} {
		if got := firstMetadata(received, key); got != want {
			t.Fatalf("metadata %s = %q, want %q", key, got, want)
		}
	}
}

func TestProxyRejectsCapsetOutsideAllowedSet(t *testing.T) {
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error { return nil })
	defer stopOcto()
	proxyAddr, stopProxy := startTestProxy(t, Config{Listen: "127.0.0.1:0", OctoBus: staticOctoBus(octoAddr, "")}, testResolver{binding: SessionBinding{SessionID: "s1", CapsetIDs: []string{"dev"}}})
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Guest requests a capset the session is not allowed to use.
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.MD{
		SessionTokenMetadata: []string{"session-token"},
		"x-octobus-capset":   []string{"other"},
	})
	out := rawFrame(nil)
	err = conn.Invoke(ctx, "/pkg.Service/Call", rawFrame("ping"), &out)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for disallowed capset, got %v", err)
	}
}

func TestProxyRejectsMissingSessionToken(t *testing.T) {
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error { return nil })
	defer stopOcto()
	proxyAddr, stopProxy := startTestProxy(t, Config{Listen: "127.0.0.1:0", OctoBus: staticOctoBus(octoAddr, "")}, testResolver{binding: SessionBinding{SessionID: "s1", CapsetIDs: []string{"dev"}}})
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	out := rawFrame(nil)
	err = conn.Invoke(context.Background(), "/pkg.Service/Call", rawFrame("ping"), &out)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %s, want %s; err=%v", status.Code(err), codes.Unauthenticated, err)
	}
}

func startTestProxy(t *testing.T, config Config, resolver SessionResolver) (string, func()) {
	return startTestProxyWithBindings(t, config, resolver, testBindingResolver{})
}

func startTestProxyWithBindings(t *testing.T, config Config, resolver SessionResolver, bindings BindingResolver) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", config.Listen)
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	config.Listen = addr
	server := NewServer(config, resolver, bindings)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- server.serve(ctx, ln) }()
	return addr, func() {
		cancel()
		if err := <-errCh; err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Fatalf("proxy returned error: %v", err)
		}
	}
}

func startTestRawGRPC(t *testing.T, handler grpc.StreamHandler) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.ForceServerCodec(rawCodec{}), grpc.UnknownServiceHandler(func(srv any, stream grpc.ServerStream) error {
		return handler(srv, stream)
	}))
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ln) }()
	return ln.Addr().String(), func() {
		server.Stop()
		if err := <-errCh; err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Fatalf("raw grpc returned error: %v", err)
		}
	}
}
