package node

import (
	"context"
	"time"
)

// Provisioner installs and configures Docker on a remote node.
type Provisioner struct {
	dialer *Dialer
	logs   func(string)
}

// NewProvisioner builds a provisioner for a node dialer.
func NewProvisioner(dialer *Dialer) *Provisioner {
	return &Provisioner{dialer: dialer}
}

// OnProgress receives install output.
func (p *Provisioner) OnProgress(fn func(string)) { p.logs = fn }

// Install uploads and runs the Docker install script on the node.
//
// The whole operation is bounded by a generous timeout because Docker's install
// script downloads a distribution's packages. The script is idempotent, so a
// timeout leaves the node in a re-runnable state.
func (p *Provisioner) Install(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	const remotePath = "/tmp/meghify-install-docker.sh"
	if err := p.dialer.UploadString(ctx, InstallScript, remotePath, 0o700); err != nil {
		return errCreating(err)
	}
	return p.dialer.Run(ctx, "bash "+remotePath, p.logs)
}

func errCreating(err error) error { return &wrapError{"upload install script", err} }

type wrapError struct {
	prefix string
	err    error
}

func (e *wrapError) Error() string {
	if e.err == nil {
		return e.prefix
	}
	return e.prefix + ": " + e.err.Error()
}

func (e *wrapError) Unwrap() error { return e.err }
