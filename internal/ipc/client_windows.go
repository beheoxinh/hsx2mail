//go:build windows

package ipc

import (
	"context"
	"fmt"
	"time"

	"github.com/Microsoft/go-winio"
)

// PipeClient implements the Client interface using Windows named pipes.
type PipeClient struct {
	*BaseClient
	address string
}

// NewPipeClient creates a new Windows named pipe client.
func NewPipeClient(address string) *PipeClient {
	return &PipeClient{
		BaseClient: NewBaseClient(),
		address:    address,
	}
}

// Connect establishes a connection to the server and authenticates.
func (c *PipeClient) Connect(ctx context.Context, token string) error {
	// Set connection timeout
	timeout := ConnectTimeout

	conn, err := winio.DialPipeContext(ctx, c.address)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	// Set deadline for the connection attempt
	conn.SetDeadline(time.Now().Add(timeout))
	defer conn.SetDeadline(time.Time{})

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
func (c *PipeClient) Ping(ctx context.Context) error {
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
// On Windows, this returns a PipeClient.
func NewClient(address string) Client {
	return NewPipeClient(address)
}
