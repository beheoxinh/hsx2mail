//go:build linux || darwin

package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// UnixServer implements the Server interface using Unix domain sockets.
type UnixServer struct {
	*BaseServer
	socketPath string
}

// NewUnixServer creates a new Unix socket server.
func NewUnixServer(tokenMgr *TokenManager) *UnixServer {
	s := &UnixServer{
		BaseServer: NewBaseServer(tokenMgr),
	}
	// Pre-compute socket path so Address() works before Start()
	if path, err := s.createSocketPath(); err == nil {
		s.socketPath = path
		s.BaseServer.address = path
	}
	return s
}

// Start begins listening on the Unix socket.
func (s *UnixServer) Start(ctx context.Context) error {
	socketPath, err := s.createSocketPath()
	if err != nil {
		return fmt.Errorf("failed to create socket path: %w", err)
	}
	s.socketPath = socketPath

	// Remove existing socket if present
	os.Remove(socketPath)

	// Set umask before creating socket to avoid TOCTOU race with Chmod
	oldMask := syscall.Umask(0077)
	listener, err := net.Listen("unix", socketPath)
	syscall.Umask(oldMask)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	s.SetListener(listener, socketPath)

	return s.AcceptLoop(ctx)
}

// Stop gracefully shuts down the server and removes the socket file.
func (s *UnixServer) Stop() error {
	err := s.BaseServer.Stop()

	// Clean up socket file
	if s.socketPath != "" {
		os.Remove(s.socketPath)
	}

	return err
}

// createSocketPath creates the socket directory and returns the socket path.
func (s *UnixServer) createSocketPath() (string, error) {
	// Use /tmp/hsx2mail-{uid}/ directory
	uid := os.Getuid()
	socketDir := filepath.Join(os.TempDir(), fmt.Sprintf("hsx2mail-%d", uid))

	// Create directory with restrictive permissions (0700)
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Verify directory permissions
	info, err := os.Stat(socketDir)
	if err != nil {
		return "", fmt.Errorf("failed to stat socket directory: %w", err)
	}

	if info.Mode().Perm() != 0700 {
		if err := os.Chmod(socketDir, 0700); err != nil {
			return "", fmt.Errorf("failed to set directory permissions: %w", err)
		}
	}

	return filepath.Join(socketDir, "ipc.sock"), nil
}

// NewServer creates a new platform-appropriate IPC server.
// On Unix systems (Linux/macOS), this returns a UnixServer.
func NewServer(tokenMgr *TokenManager) Server {
	return NewUnixServer(tokenMgr)
}
