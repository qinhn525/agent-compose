package agentcompose

import (
	"agent-compose/pkg/agentcompose/domain"
	"agent-compose/pkg/agentcompose/execution"
	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"context"
	"fmt"

	"github.com/samber/do/v2"
)

type SessionVMInfo = domain.SessionVMInfo

type BoxRuntime interface {
	EnsureSession(context.Context, *Session, VMState, ProxyState) (SessionVMInfo, error)
	StopSession(context.Context, *Session, VMState) (bool, error)
	Exec(context.Context, *Session, VMState, ExecSpec) (ExecResult, error)
	ExecStream(context.Context, *Session, VMState, ExecSpec, ExecStreamWriter) (ExecResult, error)
}

type sessionAliveRuntime interface {
	IsSessionAlive(context.Context, *Session, VMState) (bool, error)
}

type RuntimeProvider interface {
	ForDriver(string) (BoxRuntime, error)
	ForSession(*Session) (BoxRuntime, error)
}

type runtimeProvider struct {
	config   *appconfig.Config
	runtimes map[string]BoxRuntime
}

type driverRuntimeAdapter struct {
	runtime driverpkg.BoxRuntime
}

func NewRuntimeProvider(di do.Injector) (RuntimeProvider, error) {
	config := do.MustInvoke[*appconfig.Config](di)
	boxliteRuntime, err := driverpkg.NewBoxliteRuntime(config)
	if err != nil {
		return nil, err
	}
	dockerRuntime, err := driverpkg.NewDockerRuntime(config)
	if err != nil {
		return nil, err
	}
	microsandboxRuntime, err := driverpkg.NewMicrosandboxRuntime(config)
	if err != nil {
		return nil, err
	}
	return &runtimeProvider{
		config: config,
		runtimes: map[string]BoxRuntime{
			driverpkg.RuntimeDriverBoxlite:      driverRuntimeAdapter{runtime: boxliteRuntime},
			driverpkg.RuntimeDriverDocker:       driverRuntimeAdapter{runtime: dockerRuntime},
			driverpkg.RuntimeDriverMicrosandbox: driverRuntimeAdapter{runtime: microsandboxRuntime},
		},
	}, nil
}

func (p *runtimeProvider) ForDriver(driver string) (BoxRuntime, error) {
	driver = driverpkg.ResolveRuntimeDriver(driver)
	if err := driverpkg.ValidateRuntimeDriver(driver); err != nil {
		return nil, err
	}
	runtime, ok := p.runtimes[driver]
	if !ok {
		return nil, fmt.Errorf("agent-compose runtime %q is not configured", driver)
	}
	return runtime, nil
}

func (p *runtimeProvider) ForSession(session *Session) (BoxRuntime, error) {
	if session == nil {
		return nil, fmt.Errorf("session is required")
	}
	driver, err := driverpkg.ResolveSessionRuntimeDriver(session.Summary.Driver, p.config.RuntimeDriver)
	if err != nil {
		return nil, err
	}
	return p.ForDriver(driver)
}

func (r driverRuntimeAdapter) EnsureSession(ctx context.Context, session *Session, vmState VMState, proxyState ProxyState) (SessionVMInfo, error) {
	info, err := r.runtime.EnsureSession(ctx, toDriverSession(session), toDriverVMState(vmState), toDriverProxyState(proxyState))
	if err != nil {
		return SessionVMInfo{}, err
	}
	return fromDriverSessionVMInfo(info), nil
}

func (r driverRuntimeAdapter) StopSession(ctx context.Context, session *Session, vmState VMState) (bool, error) {
	return r.runtime.StopSession(ctx, toDriverSession(session), toDriverVMState(vmState))
}

func (r driverRuntimeAdapter) Exec(ctx context.Context, session *Session, vmState VMState, spec ExecSpec) (ExecResult, error) {
	result, err := r.runtime.Exec(ctx, toDriverSession(session), toDriverVMState(vmState), toDriverExecSpec(spec))
	return fromDriverExecResult(result), err
}

func (r driverRuntimeAdapter) ExecStream(ctx context.Context, session *Session, vmState VMState, spec ExecSpec, stream ExecStreamWriter) (ExecResult, error) {
	driverStream := func(chunk driverpkg.ExecChunk) {
		if stream != nil {
			stream(ExecChunk{Text: chunk.Text, IsStderr: chunk.IsStderr})
		}
	}
	result, err := r.runtime.ExecStream(ctx, toDriverSession(session), toDriverVMState(vmState), toDriverExecSpec(spec), driverStream)
	return fromDriverExecResult(result), err
}

func (r driverRuntimeAdapter) IsSessionAlive(ctx context.Context, session *Session, vmState VMState) (bool, error) {
	aliveRuntime, ok := r.runtime.(interface {
		IsSessionAlive(context.Context, *driverpkg.Session, driverpkg.VMState) (bool, error)
	})
	if !ok {
		return false, fmt.Errorf("runtime does not support session liveness checks")
	}
	return aliveRuntime.IsSessionAlive(ctx, toDriverSession(session), toDriverVMState(vmState))
}

func toDriverSession(session *Session) *driverpkg.Session {
	return execution.ToDriverSession(session)
}

func toDriverVMState(state VMState) driverpkg.VMState {
	return execution.ToDriverVMState(state)
}

func fromDriverVMState(state driverpkg.VMState) VMState {
	return execution.FromDriverVMState(state)
}

func toDriverProxyState(state ProxyState) driverpkg.ProxyState {
	return execution.ToDriverProxyState(state)
}

func toDriverExecSpec(spec ExecSpec) driverpkg.ExecSpec {
	return execution.ToDriverExecSpec(spec)
}

func fromDriverSessionVMInfo(info driverpkg.SessionVMInfo) SessionVMInfo {
	return execution.FromDriverSessionVMInfo(info)
}

func fromDriverExecResult(result driverpkg.ExecResult) ExecResult {
	return execution.FromDriverExecResult(result)
}
