package undo

import (
	"context"
	"testing"
	"time"
)

// stubUndoContext records the moves the commands ask for.
type stubUndoContext struct {
	UndoContext // embedded nil: only the methods used here are implemented
	moves       []string
	findErr     error
	findErrFor  string
	findEmpty   bool
	findIDs     map[string][]string
}

func (s *stubUndoContext) FindLocalMessageIDs(accountID, destFolderID string, rfc822IDs []string) ([]string, error) {
	if s.findErr != nil && s.findErrFor == destFolderID {
		return nil, s.findErr
	}
	if s.findEmpty {
		return nil, nil
	}
	return s.findIDs[destFolderID], nil
}

func (s *stubUndoContext) MoveMessagesToFolder(messageIDs []string, destFolderID string) error {
	s.moves = append(s.moves, destFolderID)
	return nil
}

func newTestMoveCommand(ctx *stubUndoContext, source, dest string) *MoveCommand {
	return NewMoveCommand(ctx, "acct-1", []string{"<a@x>"}, source, dest, "Move to "+dest)
}

// A cross-account move fans out into one command per account. Undoing must take
// a single press and reverse every partition, not just the first.
func TestMultiMoveCommand_UndoReversesEveryPartition(t *testing.T) {
	stub := &stubUndoContext{
		findIDs: map[string][]string{
			"dest-a": {"local-1"},
			"dest-b": {"local-2"},
			"dest-c": {"local-3"},
		},
	}
	cmd := NewMultiMoveCommand([]MoveCommand{
		*newTestMoveCommand(stub, "src-a", "dest-a"),
		*newTestMoveCommand(stub, "src-b", "dest-b"),
		*newTestMoveCommand(stub, "src-c", "dest-c"),
	}, "Move to Archive")

	if err := cmd.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(stub.moves) != 3 {
		t.Fatalf("expected 3 reversed partitions, got %d (%v)", len(stub.moves), stub.moves)
	}
	for i, want := range []string{"src-a", "src-b", "src-c"} {
		if stub.moves[i] != want {
			t.Errorf("partition %d moved to %q, want %q", i, stub.moves[i], want)
		}
	}
}

// A failure in one partition must not stop the others: the user is left with
// fewer undone partitions, not with the remaining accounts still moved and no
// way to retry.
func TestMultiMoveCommand_ContinuesAfterAPartitionFails(t *testing.T) {
	stub := &stubUndoContext{
		findErr:    errBoom,
		findErrFor: "dest-a",
		findIDs:    map[string][]string{"dest-b": {"local-2"}},
	}
	cmd := NewMultiMoveCommand([]MoveCommand{
		*newTestMoveCommand(stub, "src-a", "dest-a"),
		*newTestMoveCommand(stub, "src-b", "dest-b"),
	}, "Move to Archive")

	err := cmd.Undo()
	if err == nil {
		t.Error("expected an error for the failing partition")
	}
	if len(stub.moves) != 1 || stub.moves[0] != "src-b" {
		t.Errorf("expected the working partition to still be reversed, got %v", stub.moves)
	}
}

type boom struct{}

func (boom) Error() string { return "boom" }

var errBoom = boom{}

// A partition whose messages no longer resolve is reported, not silently passed.
func TestMultiMoveCommand_ReportsUnresolvablePartition(t *testing.T) {
	stub := &stubUndoContext{findEmpty: true}
	cmd := NewMultiMoveCommand([]MoveCommand{
		*newTestMoveCommand(stub, "src-a", "dest-a"),
	}, "Move to Archive")

	if err := cmd.Undo(); err == nil {
		t.Error("expected an error when a partition resolves to no messages")
	}
}

// Discard must remove exactly the given command even when other commands were
// pushed after it — the pop-the-top approach silently deleted the wrong action.
func TestStackDiscard_RemovesOnlyTheTargetCommand(t *testing.T) {
	stack := NewStack(10, time.Minute)

	stub := &stubUndoContext{findIDs: map[string][]string{"dest": {"local-1"}}}
	first := NewMoveCommand(stub, "acct-1", []string{"<1@x>"}, "src-1", "dest", "first")
	second := NewMoveCommand(stub, "acct-1", []string{"<2@x>"}, "src-2", "dest", "second")
	third := NewMoveCommand(stub, "acct-1", []string{"<3@x>"}, "src-3", "dest", "third")

	stack.Push(first)
	stack.Push(second)
	stack.Push(third)

	// Undo the MIDDLE one, as App.Undo does when a background action pushed
	// after the peek.
	stack.Discard(second)

	if got := len(stack.commands); got != 2 {
		t.Fatalf("stack size = %d, want 2", got)
	}
	if stack.commands[0] != Command(first) {
		t.Error("Discard removed the wrong command (first was dropped)")
	}
	if stack.commands[1] != Command(third) {
		t.Error("Discard removed the wrong command (third was dropped)")
	}
}

func TestStackDiscard_UnknownCommandIsANoOp(t *testing.T) {
	stack := NewStack(10, time.Minute)
	stub := &stubUndoContext{}
	kept := NewMoveCommand(stub, "acct-1", []string{"<1@x>"}, "src", "dest", "kept")
	stack.Push(kept)

	stack.Discard(NewMoveCommand(stub, "acct-1", []string{"<2@x>"}, "src", "dest", "other"))

	if len(stack.commands) != 1 || stack.commands[0] != Command(kept) {
		t.Error("Discard of an unknown command changed the stack")
	}
}

// The undo path re-enters the move pipeline, which must not push a new command
// (that would turn Undo into a toggle). This pins the pushUndo=false wiring.
func TestMoveMessagesForUndo_DoesNotReenterUndoContext(t *testing.T) {
	stub := &stubUndoContext{findIDs: map[string][]string{"dest": {"local-1"}}}
	cmd := newTestMoveCommand(stub, "src", "dest")
	if err := cmd.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(stub.moves) != 1 || stub.moves[0] != "src" {
		t.Fatalf("moves = %v, want [src]", stub.moves)
	}
	_ = context.Background()
}
