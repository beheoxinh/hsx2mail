package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// newTestServer starts a BaseServer on a loopback TCP listener.
func newTestServer(t *testing.T) (*BaseServer, string) {
	t.Helper()

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()

	s := NewBaseServer(tm)
	s.SetListener(ln, addr)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.AcceptLoop(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = s.Stop()
	})

	return s, addr
}

// liveConns reports how many connections the server currently tracks.
func liveConns(s *BaseServer) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conns
}

func waitForLiveConns(t *testing.T, s *BaseServer, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if liveConns(s) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("server tracked %d connections, want %d", liveConns(s), want)
}

func readAuthResponse(t *testing.T, dec *json.Decoder) AuthResponsePayload {
	t.Helper()
	var resp Message
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if resp.Type != TypeAuthResponse {
		t.Fatalf("expected %q, got %q", TypeAuthResponse, resp.Type)
	}
	var payload AuthResponsePayload
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	return payload
}

// TestAuthFrameFitsCap keeps the pre-auth cap honest: the largest legitimate
// handshake must still fit.
func TestAuthFrameFitsCap(t *testing.T) {
	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	raw, err := json.Marshal(Message{
		ID:      "00000000-0000-4000-8000-000000000000",
		Type:    TypeAuth,
		Payload: mustMarshal(t, AuthPayload{Token: tm.GetToken()}),
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	t.Logf("largest legitimate auth frame = %d bytes, cap = %d", len(raw), MaxAuthFrameBytes)
	if len(raw) >= MaxAuthFrameBytes {
		t.Fatalf("auth frame (%d B) does not fit under MaxAuthFrameBytes (%d B)", len(raw), MaxAuthFrameBytes)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return b
}

// TestOversizedAuthFrameRejected covers 5-01.
func TestOversizedAuthFrameRejected(t *testing.T) {
	_, addr := newTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	frame := map[string]any{
		"type":    TypeAuth,
		"id":      "00000000-0000-4000-8000-000000000000",
		"payload": map[string]any{"token": strings.Repeat("A", MaxAuthFrameBytes*4)},
	}
	if err := json.NewEncoder(conn).Encode(frame); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	payload := readAuthResponse(t, json.NewDecoder(bufio.NewReader(conn)))
	if payload.Success {
		t.Fatal("oversized auth frame was accepted")
	}
	if payload.Error != "auth frame too large" {
		t.Fatalf(`expected "auth frame too large", got %q`, payload.Error)
	}
}

// TestConnectionCapEnforced covers 5-02.
func TestConnectionCapEnforced(t *testing.T) {
	s, addr := newTestServer(t)

	for i := 0; i < MaxConnections; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("Dial %d: %v", i, err)
		}
		defer c.Close()
	}
	waitForLiveConns(t, s, MaxConnections)

	extra, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial extra: %v", err)
	}
	defer extra.Close()
	_ = extra.SetDeadline(time.Now().Add(5 * time.Second))

	if err := json.NewEncoder(extra).Encode(Message{ID: "1", Type: TypePing}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var resp Message
	if err := json.NewDecoder(bufio.NewReader(extra)).Decode(&resp); err == nil {
		t.Fatalf("connection %d was served with %q, want rejection", MaxConnections+1, resp.Type)
	}
}

// TestConnectionCapReleasesSlot proves the cap counts live connections only.
func TestConnectionCapReleasesSlot(t *testing.T) {
	s, addr := newTestServer(t)

	held := make([]net.Conn, 0, MaxConnections)
	for i := 0; i < MaxConnections; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("Dial %d: %v", i, err)
		}
		held = append(held, c)
	}
	waitForLiveConns(t, s, MaxConnections)

	held[0].Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && liveConns(s) >= MaxConnections {
		time.Sleep(5 * time.Millisecond)
	}
	for _, c := range held[1:] {
		defer c.Close()
	}

	replacement, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial replacement: %v", err)
	}
	defer replacement.Close()
	waitForLiveConns(t, s, MaxConnections)
}

// TestNormalSizeAuthAccepted guards the cap against breaking real handshakes.
func TestNormalSizeAuthAccepted(t *testing.T) {
	s, addr := newTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	msg, err := NewMessage(TypeAuth, AuthPayload{Token: s.tokenMgr.GetToken()})
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	dec := json.NewDecoder(bufio.NewReader(conn))
	if payload := readAuthResponse(t, dec); !payload.Success {
		t.Fatalf("valid auth rejected: %q", payload.Error)
	}

	if err := json.NewEncoder(conn).Encode(Message{ID: "1", Type: TypePing}); err != nil {
		t.Fatalf("Encode ping: %v", err)
	}
	var pong Message
	if err := dec.Decode(&pong); err != nil {
		t.Fatalf("Decode pong: %v", err)
	}
	if pong.Type != TypePong {
		t.Fatalf("expected %q, got %q", TypePong, pong.Type)
	}
}
