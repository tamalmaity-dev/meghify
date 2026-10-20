// Package node provisions and reaches remote servers.
//
// The panel follows the Coolify model: SSH into the target, install Docker, then
// talk to the remote Docker Engine through a local SSH tunnel. No cluster or
// overlay network is required, and each node stays independently manageable even
// if the panel is offline.
package node

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/docker"
	"golang.org/x/crypto/ssh"
)

// KeyPair is a generated SSH credential.
type KeyPair struct {
	PrivatePEM  string
	PublicLine  string
	Fingerprint string
}

// GenerateKeyPair creates an ed25519 key pair.
func GenerateKeyPair() (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519: %w", err)
	}

	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, err
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return &KeyPair{
		PrivatePEM:  string(pem.EncodeToMemory(block)),
		PublicLine:  strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))),
		Fingerprint: ssh.FingerprintSHA256(sshPub),
	}, nil
}

// InstallScript is run on the node to install Docker.
//
// It is idempotent so re-provisioning an existing node is safe.
const InstallScript = `#!/usr/bin/env bash
set -euo pipefail

if command -v docker >/dev/null 2>&1; then
  echo "docker already installed"
else
  echo "installing docker"
  curl -fsSL https://get.docker.com | sh
fi

systemctl enable --now docker 2>/dev/null || service docker start || true

# Expose the engine on loopback with a self-signed certificate so the panel can
# reach it through the SSH tunnel without opening a public port.
CERT_DIR=/etc/meghify-tls
mkdir -p "$CERT_DIR"
if [ ! -f "$CERT_DIR/server-cert.pem" ]; then
  openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
    -keyout "$CERT_DIR/server-key.pem" -out "$CERT_DIR/server-cert.pem" \
    -subj "/CN=$(hostname)" -addext "subjectAltName=IP:127.0.0.1,DNS:localhost"
  chmod 600 "$CERT_DIR/server-key.pem"
fi

mkdir -p /etc/systemd/system/docker.service.d
cat > /etc/systemd/system/docker.service.d/meghify.conf <<'UNIT'
[Service]
ExecStart=
ExecStart=/usr/bin/dockerd -H fd:// -H tcp://127.0.0.1:2376 --tlsverify \
  --tlscacert=/etc/meghify-tls/server-cert.pem --tlscert=/etc/meghify-tls/server-cert.pem \
  --tlskey=/etc/meghify-tls/server-key.pem
UNIT

systemctl daemon-reload
systemctl restart docker

echo "docker ready"
docker version --format '{{.Server.Version}}'
`

// Dialer opens SSH connections to a node.
type Dialer struct {
	Address    string
	Port       int
	User       string
	PrivateKey string // PEM encoded

	// HostKey is the pinned SHA256 fingerprint. Empty enables trust on first
	// use: the fingerprint observed during the handshake is reported through
	// OnHostKey and becomes the pin for every later connection.
	HostKey   string
	OnHostKey func(ctx context.Context, fingerprint string) error

	mu     sync.Mutex
	client *ssh.Client
}

// NewDialer builds a dialer for a node.
func NewDialer(address string, port int, user, privateKeyPEM, hostKey string, onHostKey func(context.Context, string) error) (*Dialer, error) {
	if address == "" {
		return nil, errors.New("node address is required")
	}
	if port == 0 {
		port = 22
	}
	if user == "" {
		user = "root"
	}
	if strings.TrimSpace(privateKeyPEM) == "" {
		return nil, errors.New("node private key is required")
	}
	return &Dialer{
		Address:    address,
		Port:       port,
		User:       user,
		PrivateKey: privateKeyPEM,
		HostKey:    strings.TrimSpace(hostKey),
		OnHostKey:  onHostKey,
	}, nil
}

// Connect opens the SSH client, reusing a live one.
func (d *Dialer) Connect(ctx context.Context) (*ssh.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		// A keepalive request tells us whether the connection is still usable.
		if _, _, err := d.client.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			return d.client, nil
		}
		d.client.Close()
		d.client = nil
	}

	signer, err := ssh.ParsePrivateKey([]byte(d.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	addr := net.JoinHostPort(d.Address, fmt.Sprint(d.Port))
	config := &ssh.ClientConfig{
		User: d.User,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			fingerprint := ssh.FingerprintSHA256(key)
			if d.HostKey != "" {
				if subtle.ConstantTimeCompare([]byte(d.HostKey), []byte(fingerprint)) != 1 {
					return fmt.Errorf("ssh host key mismatch for %s: expected %s, got %s",
						d.Address, d.HostKey, fingerprint)
				}
				return nil
			}
			// Trust on first use. Persist before accepting so a database
			// failure cannot silently downgrade every later connection to TOFU.
			if d.OnHostKey == nil {
				return fmt.Errorf("ssh host key for %s is not pinned and no persistence sink is configured", d.Address)
			}
			if err := d.OnHostKey(ctx, fingerprint); err != nil {
				return fmt.Errorf("record ssh host key for %s: %w", d.Address, err)
			}
			d.HostKey = fingerprint
			return nil
		},
		Timeout: 10 * time.Second,
	}

	d.client, err = ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	return d.client, nil
}

// Run executes a command over SSH, streaming output to sink.
func (d *Dialer) Run(ctx context.Context, command string, sink func(string)) error {
	client, err := d.Connect(ctx)
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	session.Stderr = session.Stdout

	if err := session.Start(command); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, readErr := stdout.Read(buf)
			if n > 0 && sink != nil {
				sink(string(buf[:n]))
			}
			if readErr != nil {
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		session.Close()
		return ctx.Err()
	case <-done:
		return session.Wait()
	}
}

// Shell is an interactive pty session on a node.
type Shell struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
}

// OpenShell starts an interactive login shell over SSH with a pty attached.
// Reads are the shell's output, writes are its input, and Resize follows the
// browser's terminal dimensions.
func (d *Dialer) OpenShell(ctx context.Context, rows, cols uint) (*Shell, error) {
	client, err := d.Connect(ctx)
	if err != nil {
		return nil, err
	}

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh session: %w", err)
	}

	if rows == 0 {
		rows = 24
	}
	if cols == 0 {
		cols = 80
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := session.RequestPty("xterm-256color", int(rows), int(cols), modes); err != nil {
		session.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	// With a pty the remote shell writes stdout and stderr to the same stream.

	if err := session.Shell(); err != nil {
		session.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}
	return &Shell{session: session, stdin: stdin, stdout: stdout}, nil
}

// Read returns output produced by the shell.
func (s *Shell) Read(p []byte) (int, error) { return s.stdout.Read(p) }

// Write feeds input to the shell.
func (s *Shell) Write(p []byte) (int, error) { return s.stdin.Write(p) }

// Resize updates the pty window to match the browser terminal.
func (s *Shell) Resize(rows, cols uint) error {
	if rows == 0 || cols == 0 {
		return nil
	}
	return s.session.WindowChange(int(rows), int(cols))
}

// Close ends the shell session.
func (s *Shell) Close() error { return s.session.Close() }

// UploadString writes content to a remote path using a shell heredoc.
func (d *Dialer) UploadString(ctx context.Context, content, path string, mode os.FileMode) error {
	script := fmt.Sprintf("cat > %s <<'PASS_EOF'\n%s\nPASS_EOF\nchmod %o %s\n",
		path, content, mode.Perm(), path)
	return d.Run(ctx, script, nil)
}

// Close tears down the SSH connection.
func (d *Dialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client == nil {
		return nil
	}
	err := d.client.Close()
	d.client = nil
	return err
}

// Tunnel exposes the node's Docker Engine on a loopback port.
type Tunnel struct {
	listener net.Listener
	port     int
	closed   bool
	mu       sync.Mutex
}

// OpenTunnel starts a local listener that forwards to the remote Docker port
// over the SSH connection.
func OpenTunnel(dialer *Dialer, remotePort int) (*Tunnel, error) {
	if remotePort == 0 {
		remotePort = 2376
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for tunnel: %w", err)
	}

	t := &Tunnel{listener: listener, port: listener.Addr().(*net.TCPAddr).Port}
	go t.serve(dialer, remotePort)
	return t, nil
}

func (t *Tunnel) serve(dialer *Dialer, remotePort int) {
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			return
		}
		go t.forward(conn, dialer, remotePort)
	}
}

func (t *Tunnel) forward(local net.Conn, dialer *Dialer, remotePort int) {
	defer local.Close()

	client, err := dialer.Connect(context.Background())
	if err != nil {
		return
	}
	remote, err := client.Dial("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(remotePort)))
	if err != nil {
		return
	}
	defer remote.Close()

	errc := make(chan error, 2)
	go func() { _, err := io.Copy(remote, local); errc <- err }()
	go func() { _, err := io.Copy(local, remote); errc <- err }()
	<-errc
}

// LocalAddr returns host:port of the tunnel, ready for a Docker client.
func (t *Tunnel) LocalAddr() string {
	return fmt.Sprintf("127.0.0.1:%d", t.port)
}

// Close stops the tunnel.
func (t *Tunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	return t.listener.Close()
}

// NewDockerClient returns an engine client for a tunnelled node. The remote
// daemon presents a self-signed certificate, so verification is skipped.
func NewDockerClient(addr string) (*docker.Client, error) {
	return docker.NewClientFromTunnel(addr)
}
