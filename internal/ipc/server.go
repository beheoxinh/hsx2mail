package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// AuthTimeout is the maximum time allowed for a client to authenticate
	AuthTimeout = 5 * time.Second

	// ReadBufferSize is the buffer size for reading messages
	ReadBufferSize = 64 * 1024
)

// serverClient represents a connected client.
type serverClient struct {
	id            string
	conn          net.Conn
	authenticated bool
	encoder       *json.Encoder
	mu            sync.Mutex // Protects encoder writes
}

// BaseServer provides common server functionality used by both
// Unix socket and Windows named pipe implementations.
type BaseServer struct {
	tokenMgr *TokenManager
	handler  MessageHandler
	clients  map[string]*serverClient
	mu       sync.RWMutex
	address  string
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewBaseServer creates a new BaseServer with the given token manager.
func NewBaseServer(tokenMgr *TokenManager) *BaseServer {
	return &BaseServer{
		tokenMgr: tokenMgr,
		clients:  make(map[string]*serverClient),
	}
}

// Address returns the address clients should connect to.
func (s *BaseServer) Address() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.address
}

// OnMessage registers a handler for incoming messages.
func (s *BaseServer) OnMessage(handler MessageHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = handler
}

// Send sends a message to a specific client.
func (s *BaseServer) Send(clientID string, msg Message) error {
	s.mu.RLock()
	client, ok := s.clients[clientID]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("client %s not found", clientID)
	}

	if !client.authenticated {
		return fmt.Errorf("client %s not authenticated", clientID)
	}

	return s.sendToClient(client, msg)
}

// Broadcast sends a message to all authenticated clients.
func (s *BaseServer) Broadcast(msg Message) error {
	s.mu.RLock()
	clients := make([]*serverClient, 0, len(s.clients))
	for _, client := range s.clients {
		if client.authenticated {
			clients = append(clients, client)
		}
	}
	s.mu.RUnlock()

	var lastErr error
	for _, client := range clients {
		if err := s.sendToClient(client, msg); err != nil {
			slog.Warn("failed to send broadcast to client",
				"client_id", client.id,
				"error", err)
			lastErr = err
		}
	}

	return lastErr
}

// Clients returns the IDs of all currently connected and authenticated clients.
func (s *BaseServer) Clients() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.clients))
	for id, client := range s.clients {
		if client.authenticated {
			ids = append(ids, id)
		}
	}
	return ids
}

// Stop gracefully shuts down the server.
func (s *BaseServer) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}

	// Close the listener to stop accepting new connections
	if s.listener != nil {
		s.listener.Close()
	}

	// Close all client connections
	s.mu.Lock()
	for _, client := range s.clients {
		client.conn.Close()
	}
	s.clients = make(map[string]*serverClient)
	s.mu.Unlock()

	// Wait for all goroutines to finish
	s.wg.Wait()

	return nil
}

// SetListener sets the network listener and address.
// Called by platform-specific implementations after creating the listener.
func (s *BaseServer) SetListener(listener net.Listener, address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listener = listener
	s.address = address
}

// AcceptLoop accepts incoming connections until the context is cancelled.
// This should be called by platform-specific implementations from Start().
func (s *BaseServer) AcceptLoop(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return nil // Normal shutdown
			default:
				slog.Error("failed to accept connection", "error", err)
				continue
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConnection(conn)
		}()
	}
}

// handleConnection manages a single client connection.
func (s *BaseServer) handleConnection(conn net.Conn) {
	clientID := uuid.New().String()
	client := &serverClient{
		id:      clientID,
		conn:    conn,
		encoder: json.NewEncoder(conn),
	}

	s.mu.Lock()
	s.clients[clientID] = client
	s.mu.Unlock()

	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.clients, clientID)
		s.mu.Unlock()
		slog.Debug("client disconnected", "client_id", clientID)
	}()

	slog.Debug("new client connected", "client_id", clientID)

	// Authenticate with timeout
	if !s.authenticateClient(client) {
		slog.Warn("client authentication failed", "client_id", clientID)
		return
	}

	slog.Debug("client authenticated", "client_id", clientID)

	// Read messages
	s.readLoop(client)
}

// authenticateClient handles the authentication handshake.
func (s *BaseServer) authenticateClient(client *serverClient) bool {
	// Set read deadline for authentication
	_ = client.conn.SetReadDeadline(time.Now().Add(AuthTimeout))
	defer func() { _ = client.conn.SetReadDeadline(time.Time{}) }() // Clear deadline

	reader := bufio.NewReaderSize(client.conn, ReadBufferSize)
	decoder := json.NewDecoder(reader)

	var msg Message
	if err := decoder.Decode(&msg); err != nil {
		slog.Debug("failed to read auth message", "client_id", client.id, "error", err)
		return false
	}

	if msg.Type != TypeAuth {
		slog.Debug("expected auth message", "client_id", client.id, "got", msg.Type)
		s.sendAuthResponse(client, false, "expected auth message")
		return false
	}

	var payload AuthPayload
	if err := msg.ParsePayload(&payload); err != nil {
		slog.Debug("failed to parse auth payload", "client_id", client.id, "error", err)
		s.sendAuthResponse(client, false, "invalid auth payload")
		return false
	}

	if !s.tokenMgr.Validate(payload.Token) {
		slog.Debug("invalid token", "client_id", client.id)
		s.sendAuthResponse(client, false, "invalid token")
		return false
	}

	client.authenticated = true
	s.sendAuthResponse(client, true, "")
	return true
}

// sendAuthResponse sends an authentication response to the client.
func (s *BaseServer) sendAuthResponse(client *serverClient, success bool, errMsg string) {
	response, _ := NewReply(Message{}, TypeAuthResponse, AuthResponsePayload{
		Success: success,
		Error:   errMsg,
	})
	if err := s.sendToClient(client, response); err != nil {
		slog.Debug("failed to send auth response", "client_id", client.id, "error", err)
	}
}

// readLoop reads messages from a client until the connection is closed.
func (s *BaseServer) readLoop(client *serverClient) {
	reader := bufio.NewReaderSize(client.conn, ReadBufferSize)
	decoder := json.NewDecoder(reader)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		var msg Message
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				return // Client disconnected
			}
			slog.Debug("failed to read message", "client_id", client.id, "error", err)
			return
		}

		// Handle ping internally
		if msg.Type == TypePing {
			pong, _ := NewReply(msg, TypePong, nil)
			if err := s.sendToClient(client, pong); err != nil {
				slog.Debug("failed to send pong", "client_id", client.id, "error", err)
			}
			continue
		}

		// Dispatch to handler
		s.mu.RLock()
		handler := s.handler
		s.mu.RUnlock()

		if handler != nil {
			handler(client.id, msg)
		}
	}
}

// sendToClient sends a message to a client.
func (s *BaseServer) sendToClient(client *serverClient, msg Message) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.encoder.Encode(msg)
}
