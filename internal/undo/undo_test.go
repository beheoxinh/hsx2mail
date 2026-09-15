package undo

import (
	"testing"
	"time"
)

type mockCommand struct {
	BaseCommand
	executeFn func() error
	undoFn    func() error
}

func (m *mockCommand) Execute() error {
	if m.executeFn != nil {
		return m.executeFn()
	}
	return nil
}

func (m *mockCommand) Undo() error {
	if m.undoFn != nil {
		return m.undoFn()
	}
	return nil
}

func newMock(desc string) *mockCommand {
	return &mockCommand{BaseCommand: NewBaseCommand(desc)}
}

func TestNewStack(t *testing.T) {
	s := NewStack(50, 30*time.Second)
	if s == nil {
		t.Fatal("expected non-nil stack")
	}
	if s.Size() != 0 {
		t.Fatalf("expected size 0, got %d", s.Size())
	}
	if s.CanUndo() {
		t.Fatal("expected CanUndo=false for empty stack")
	}
}

func TestPushAndPop(t *testing.T) {
	s := NewStack(50, 30*time.Second)
	cmds := []*mockCommand{newMock("first"), newMock("second"), newMock("third")}
	for _, c := range cmds {
		s.Push(c)
	}

	// Pop should return in LIFO order
	for i := len(cmds) - 1; i >= 0; i-- {
		got := s.Pop()
		if got == nil {
			t.Fatalf("expected command at index %d, got nil", i)
		}
		if got.Description() != cmds[i].Description() {
			t.Fatalf("expected %q, got %q", cmds[i].Description(), got.Description())
		}
	}
}

func TestPeek(t *testing.T) {
	s := NewStack(50, 30*time.Second)
	cmd := newMock("peek-me")
	s.Push(cmd)

	peeked := s.Peek()
	if peeked == nil {
		t.Fatal("expected non-nil from Peek")
	}
	if peeked.Description() != "peek-me" {
		t.Fatalf("expected %q, got %q", "peek-me", peeked.Description())
	}
	if s.Size() != 1 {
		t.Fatalf("expected size 1 after Peek, got %d", s.Size())
	}
}

func TestClear(t *testing.T) {
	s := NewStack(50, 30*time.Second)
	s.Push(newMock("a"))
	s.Push(newMock("b"))
	s.Clear()
	if s.Size() != 0 {
		t.Fatalf("expected size 0 after Clear, got %d", s.Size())
	}
}

func TestCanUndo(t *testing.T) {
	s := NewStack(50, 30*time.Second)
	if s.CanUndo() {
		t.Fatal("expected CanUndo=false for empty stack")
	}

	s.Push(newMock("cmd"))
	if !s.CanUndo() {
		t.Fatal("expected CanUndo=true after push")
	}

	s.Pop()
	if s.CanUndo() {
		t.Fatal("expected CanUndo=false after popping all")
	}
}

func TestMaxSize(t *testing.T) {
	s := NewStack(2, 30*time.Second)
	s.Push(newMock("first"))
	s.Push(newMock("second"))
	s.Push(newMock("third"))

	if s.Size() != 2 {
		t.Fatalf("expected size 2, got %d", s.Size())
	}

	// Oldest ("first") should have been dropped
	got := s.Pop()
	if got.Description() != "third" {
		t.Fatalf("expected %q, got %q", "third", got.Description())
	}
	got = s.Pop()
	if got.Description() != "second" {
		t.Fatalf("expected %q, got %q", "second", got.Description())
	}
}

func TestExpiration(t *testing.T) {
	s := NewStack(50, 50*time.Millisecond)
	s.Push(newMock("expires"))

	time.Sleep(100 * time.Millisecond)

	if s.CanUndo() {
		t.Fatal("expected CanUndo=false after expiration")
	}
}

func TestPopEmptyStack(t *testing.T) {
	s := NewStack(50, 30*time.Second)
	got := s.Pop()
	if got != nil {
		t.Fatalf("expected nil from empty stack, got %v", got)
	}
}

func TestBaseCommand(t *testing.T) {
	before := time.Now()
	bc := NewBaseCommand("test description")
	after := time.Now()

	if bc.Description() != "test description" {
		t.Fatalf("expected %q, got %q", "test description", bc.Description())
	}

	created := bc.CreatedAt()
	if created.Before(before) || created.After(after) {
		t.Fatalf("CreatedAt %v not between %v and %v", created, before, after)
	}
}

func TestSize(t *testing.T) {
	s := NewStack(50, 30*time.Second)
	n := 5
	for i := 0; i < n; i++ {
		s.Push(newMock("cmd"))
	}
	if s.Size() != n {
		t.Fatalf("expected size %d, got %d", n, s.Size())
	}
}
