//go:build !linux

package workspace

import (
	"context"
	"time"
)

type ControlClient struct {
	SocketPath string
	Timeout    time.Duration
}

func (ControlClient) Ensure(context.Context, Identity) (Status, error) {
	return Status{}, ErrUnsupported
}

func (ControlClient) Status(context.Context, string) (Status, error) {
	return Status{}, ErrUnsupported
}

func (ControlClient) List(context.Context) ([]Status, error) {
	return nil, ErrUnsupported
}

func (ControlClient) Stop(context.Context, string) (Status, error) {
	return Status{}, ErrUnsupported
}

func (ControlClient) Remove(context.Context, string) (Status, error) {
	return Status{}, ErrUnsupported
}

func (ControlClient) Exec(context.Context, ExecRequest) (ExecResult, error) {
	return ExecResult{}, ErrUnsupported
}

func (ControlClient) OpenSession(context.Context, SessionRequest) (Session, error) {
	return nil, ErrUnsupported
}

func (ControlClient) Health(context.Context, bool) (Health, error) {
	return Health{}, ErrUnsupported
}
