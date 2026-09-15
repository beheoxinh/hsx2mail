// Package undo provides an in-memory undo system for email actions
package undo

import (
	"sync"
	"time"
)

// Command represents an undoable action
type Command interface {
	// Execute performs the action (already done, just for interface completeness)
	Execute() error
	// Undo reverses the action
	Undo() error
	// Description returns a human-readable description
	Description() string
	// CreatedAt returns when the command was created
	CreatedAt() time.Time
}

// BaseCommand provides common fields for commands
type BaseCommand struct {
	description string
	createdAt   time.Time
}

// NewBaseCommand creates a new BaseCommand with the given description
func NewBaseCommand(description string) BaseCommand {
	return BaseCommand{
		description: description,
		createdAt:   time.Now(),
	}
}

// Description returns the command description
func (c *BaseCommand) Description() string { return c.description }

// CreatedAt returns when the command was created
func (c *BaseCommand) CreatedAt() time.Time { return c.createdAt }

// Stack is an in-memory undo stack
type Stack struct {
	mu       sync.Mutex
	commands []Command
	maxSize  int
	timeout  time.Duration // How long commands remain undoable
}

// NewStack creates a new undo stack
func NewStack(maxSize int, timeout time.Duration) *Stack {
	return &Stack{
		commands: make([]Command, 0),
		maxSize:  maxSize,
		timeout:  timeout,
	}
}

// Push adds a command to the stack
func (s *Stack) Push(cmd Command) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove expired commands first
	s.cleanExpired()

	// Add new command
	s.commands = append(s.commands, cmd)

	// Trim if over max size
	if len(s.commands) > s.maxSize {
		s.commands = s.commands[len(s.commands)-s.maxSize:]
	}
}

// Pop removes and returns the most recent undoable command
// Returns nil if no commands are available or all are expired
func (s *Stack) Pop() Command {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanExpired()

	if len(s.commands) == 0 {
		return nil
	}

	cmd := s.commands[len(s.commands)-1]
	s.commands = s.commands[:len(s.commands)-1]
	return cmd
}

// Peek returns the most recent command without removing it
func (s *Stack) Peek() Command {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanExpired()

	if len(s.commands) == 0 {
		return nil
	}
	return s.commands[len(s.commands)-1]
}

// Clear removes all commands from the stack
func (s *Stack) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = make([]Command, 0)
}

// cleanExpired removes commands older than timeout (must be called with lock held)
func (s *Stack) cleanExpired() {
	cutoff := time.Now().Add(-s.timeout)
	i := 0
	for ; i < len(s.commands); i++ {
		if s.commands[i].CreatedAt().After(cutoff) {
			break
		}
	}
	if i > 0 {
		s.commands = s.commands[i:]
	}
}

// CanUndo returns true if there's a command that can be undone
func (s *Stack) CanUndo() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanExpired()
	return len(s.commands) > 0
}

// Size returns the current number of commands in the stack
func (s *Stack) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanExpired()
	return len(s.commands)
}
