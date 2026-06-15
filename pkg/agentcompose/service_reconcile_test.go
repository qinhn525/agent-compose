package agentcompose

import (
	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceReconcilePersistedSessionsMarksStalePendingFailed(t *testing.T) {
	testServiceReconcilePersistedSessionsMarksStalePendingFailed(t)
}

func TestServiceAndBridgeReconcileMicrosandboxRuntimeTypeBranches(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:                 root,
		SessionRoot:              filepath.Join(root, "sessions"),
		RuntimeDriver:            driverpkg.RuntimeDriverBoxlite,
		DefaultImage:             "debian:bookworm-slim",
		MicrosandboxDefaultImage: "devbox:archlinux",
		GuestWorkspacePath:       "/data/workspace",
		JupyterGuestPort:         8888,
		JupyterProxyBasePath:     "/agent-compose/session",
	}
	store := &Store{config: config}
	session, err := store.CreateSession(ctx, "running micro", "", driverpkg.RuntimeDriverMicrosandbox, "devbox:archlinux", "", SessionTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	session.Summary.VMStatus = VMStatusRunning
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession returned error: %v", err)
	}
	proxyState, err := store.GetProxyState(session.Summary.ID)
	if err != nil {
		t.Fatalf("GetProxyState returned error: %v", err)
	}
	proxyState.HostPort = unusedLocalTCPPort(t)
	proxyState.GuestHost = "127.0.0.1"
	if err := store.SaveProxyState(session.Summary.ID, proxyState); err != nil {
		t.Fatalf("SaveProxyState returned error: %v", err)
	}
	runtimes := fixedRuntimeProvider{runtime: &fakeLoaderAgentRuntime{}}
	service := &Service{config: config, store: store, runtimes: runtimes}
	reconciled, err := service.reconcileSessionRuntimeState(ctx, session)
	if err != nil {
		t.Fatalf("service reconcile returned error: %v", err)
	}
	if reconciled.Summary.VMStatus != VMStatusRunning {
		t.Fatalf("service reconciled status = %q", reconciled.Summary.VMStatus)
	}

	bridge := &SessionRPCBridge{config: config, store: store, runtimes: runtimes}
	reconciled, err = bridge.reconcileSessionRuntimeState(ctx, session)
	if err != nil {
		t.Fatalf("bridge reconcile returned error: %v", err)
	}
	if reconciled.Summary.VMStatus != VMStatusRunning {
		t.Fatalf("bridge reconciled status = %q", reconciled.Summary.VMStatus)
	}

	missingProxySession, err := store.CreateSession(ctx, "missing proxy", "", driverpkg.RuntimeDriverMicrosandbox, "devbox:archlinux", "", SessionTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession missing proxy returned error: %v", err)
	}
	missingProxySession.Summary.VMStatus = VMStatusRunning
	if err := store.UpdateSession(ctx, missingProxySession); err != nil {
		t.Fatalf("UpdateSession missing proxy returned error: %v", err)
	}
	if err := os.Remove(store.proxyStatePath(missingProxySession.Summary.ID)); err != nil {
		t.Fatalf("remove proxy state: %v", err)
	}
	if _, err := service.reconcileSessionRuntimeState(ctx, missingProxySession); err == nil {
		t.Fatalf("service reconcile missing proxy returned nil error")
	}
	if _, err := bridge.reconcileSessionRuntimeState(ctx, missingProxySession); err == nil {
		t.Fatalf("bridge reconcile missing proxy returned nil error")
	}
}

func testServiceReconcilePersistedSessionsMarksStalePendingFailed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:                 root,
		SessionRoot:              filepath.Join(root, "sessions"),
		RuntimeDriver:            driverpkg.RuntimeDriverBoxlite,
		DefaultImage:             "debian:bookworm-slim",
		MicrosandboxDefaultImage: "devbox:archlinux",
		GuestWorkspacePath:       "/data/workspace",
		JupyterGuestPort:         8888,
		JupyterProxyBasePath:     "/agent-compose/session",
	}
	store := &Store{config: config}
	staleSession, err := store.CreateSession(ctx, "stale", "", driverpkg.RuntimeDriverMicrosandbox, "devbox:archlinux", "", SessionTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession(stale) returned error: %v", err)
	}
	service := &Service{config: config, store: store, startedAt: time.Now().UTC()}
	freshSession, err := store.CreateSession(ctx, "fresh", "", driverpkg.RuntimeDriverMicrosandbox, "devbox:archlinux", "", SessionTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession(fresh) returned error: %v", err)
	}
	if err := service.reconcilePersistedSessions(ctx); err != nil {
		t.Fatalf("reconcilePersistedSessions returned error: %v", err)
	}
	staleLoaded, err := store.GetSession(ctx, staleSession.Summary.ID)
	if err != nil {
		t.Fatalf("GetSession(stale) returned error: %v", err)
	}
	if got, want := staleLoaded.Summary.VMStatus, VMStatusFailed; got != want {
		t.Fatalf("stale session vm status = %q, want %q", got, want)
	}
	staleVMState, err := store.GetVMState(staleSession.Summary.ID)
	if err != nil {
		t.Fatalf("GetVMState(stale) returned error: %v", err)
	}
	if got, want := staleVMState.LastError, stalePendingSessionLastError; got != want {
		t.Fatalf("stale session last error = %q, want %q", got, want)
	}
	if staleVMState.StoppedAt.IsZero() {
		t.Fatalf("expected stale pending session to record stopped_at")
	}
	events, err := store.ListEvents(ctx, staleSession.Summary.ID)
	if err != nil {
		t.Fatalf("ListEvents(stale) returned error: %v", err)
	}
	if len(events) != 1 || events[0].Type != "session.startup_interrupted" {
		t.Fatalf("stale session events = %#v, want one session.startup_interrupted event", events)
	}
	freshLoaded, err := store.GetSession(ctx, freshSession.Summary.ID)
	if err != nil {
		t.Fatalf("GetSession(fresh) returned error: %v", err)
	}
	if got, want := freshLoaded.Summary.VMStatus, VMStatusPending; got != want {
		t.Fatalf("fresh session vm status = %q, want %q", got, want)
	}
}
