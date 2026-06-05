// ssh.go — SSH frontend for the shell.
//
// Features:
//   - Generates an Ed25519 host key on first run and persists it in omnish_host_key
//   - Auth strategy: anonymous by default (any user/password accepted); prints a security warning on start
//   - PTY sessions: gliderlabs/ssh provides pty.Window; session is used directly as io.ReadWriter
//   - Shares the same Editor core with telnet and stdio
package shell

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omnish/omnish/internal/logx"
	gossh "golang.org/x/crypto/ssh"

	glssh "github.com/gliderlabs/ssh"
)

const hostKeyFile = "omnish_host_key"

// SSHServer provides an interactive SSH shell over a TCP port.
type SSHServer struct {
	reg     *Registry
	keyPath string
}

// NewSSHServer creates an SSH shell server.
// keyPath is the host private key file path (empty defaults to omnish_host_key in the executable's directory).
func NewSSHServer(reg *Registry, keyPath string) *SSHServer {
	if keyPath == "" {
		exe, err := os.Executable()
		if err == nil {
			keyPath = filepath.Join(filepath.Dir(exe), hostKeyFile)
		} else {
			keyPath = hostKeyFile
		}
	}
	return &SSHServer{reg: reg, keyPath: keyPath}
}

// ListenAndServe starts the SSH service on addr and blocks until ctx is cancelled.
func (s *SSHServer) ListenAndServe(ctx context.Context, addr string) error {
	// load or generate host key
	signer, err := s.loadOrGenerateKey()
	if err != nil {
		return fmt.Errorf("host key: %w", err)
	}

	// security warning: anonymous mode
	logx.Warn("SSH shell started in ANONYMOUS mode — any client can connect without credentials; use only on trusted networks",
		"addr", addr,
		"fingerprint", gossh.FingerprintSHA256(signer.PublicKey()),
	)

	srv := &glssh.Server{
		Addr: addr,
		Handler: func(sess glssh.Session) {
			peer := sess.RemoteAddr().String()
			logx.Info("SSH client connected", "peer", peer, "user", sess.User())

			// send welcome banner to client
			fmt.Fprint(sess, banner())

			// run shared editor loop (sess implements io.ReadWriter)
			if err := runEditorLoop(ctx, sess, sess, s.reg, defaultPrompt); err != nil && ctx.Err() == nil {
				logx.Debug("SSH session ended", "peer", peer, "err", err)
			}
			logx.Info("SSH client disconnected", "peer", peer)
		},
		// anonymous mode: accept any password
		PasswordHandler: func(ctx glssh.Context, password string) bool {
			logx.Warn("SSH anonymous login", "user", ctx.User(), "peer", ctx.RemoteAddr())
			return true
		},
		// also accept public-key connections (no passphrase required)
		PublicKeyHandler: func(ctx glssh.Context, key glssh.PublicKey) bool {
			return true
		},
		HostSigners: []glssh.Signer{signer},
	}

	// close server when ctx is cancelled
	go func() {
		<-ctx.Done()
		srv.Close() //nolint
	}()

	if err := srv.ListenAndServe(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// ── host key management ───────────────────────────────────────────────────────

// loadOrGenerateKey loads an existing key file; if absent, generates and persists a new one.
func (s *SSHServer) loadOrGenerateKey() (glssh.Signer, error) {
	// attempt to load
	if pemBytes, err := os.ReadFile(s.keyPath); err == nil {
		signer, err := gossh.ParsePrivateKey(pemBytes)
		if err == nil {
			logx.Info("SSH host key loaded", "path", s.keyPath)
			return signer, nil
		}
		logx.Warn("SSH host key parse failed, regenerating", "path", s.keyPath, "err", err)
	}

	// generate new key
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	_ = pub

	// serialize to OpenSSH PEM format
	pemBytes, err := gossh.MarshalPrivateKey(priv, "omnish host key")
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	pemData := pem.EncodeToMemory(pemBytes)

	// persist with permissions 0600
	if err := os.WriteFile(s.keyPath, pemData, 0600); err != nil {
		logx.Warn("SSH host key could not be saved", "path", s.keyPath, "err", err)
		// if we can't persist it, continue with an in-memory key
	} else {
		logx.Info("SSH host key generated and saved", "path", s.keyPath)
	}

	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	return signer, nil
}
