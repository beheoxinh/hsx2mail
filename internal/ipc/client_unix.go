//go:build linux || darwin

package ipc

import (
	"context"
	"fmt"
	"net"
	"time"
)

// UnixClient implements the Client interface using Unix domain sockets.
type UnixClient struct {
	*BaseClient
	address string
}

// NewUnixClient creates a new Unix socket client.
func NewUnixClient(address string) *UnixClient {
	return &UnixClient{
		BaseClient: NewBaseClient(),
		address:    address,
	}
}

// Connect establishes a connection to the server and authenticates.
func (c *UnixClient) Connect(ctx context.Context, token string) error {
	// Set connection timeout
	dialer := net.Dialer{
		Timeout: ConnectTimeout,
	}

	conn, err := dialer.DialContext(ctx, "unix", c.address)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	c.SetConnection(conn)

	// Authenticate
	if err := c.Authenticate(ctx, token); err != nil {
		conn.Close()
		return err
	}

	// Start reading messages
	c.StartReadLoop(ctx)

	return nil
}

// Ping sends a ping message and waits for a pong response.
func (c *UnixClient) Ping(ctx context.Context) error {
	msg, err := NewMessage(TypePing, nil)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	reply, err := c.SendAndWait(ctx, msg)
	if err != nil {
		return err
	}

	if reply.Type != TypePong {
		return fmt.Errorf("unexpected response type: %s", reply.Type)
	}

	return nil
}

// NewClient creates a new platform-appropriate IPC client.
// On Unix systems (Linux/macOS), this returns a UnixClient.
func NewClient(address string) Client {
	return NewUnixClient(address)
}
