package imap

import "errors"

var (
	// ErrNotConnected indicates the client is not connected
	ErrNotConnected = errors.New("not connected to IMAP server")

	// ErrNotAuthenticated indicates the client is not authenticated
	ErrNotAuthenticated = errors.New("not authenticated")

	// ErrConnectionClosed indicates the connection was closed
	ErrConnectionClosed = errors.New("connection closed")

	// ErrTimeout indicates an operation timed out
	ErrTimeout = errors.New("operation timed out")

	// ErrPoolExhausted indicates no connections available in the pool
	ErrPoolExhausted = errors.New("connection pool exhausted")

	// ErrAccountNotFound indicates the account was not found
	ErrAccountNotFound = errors.New("account not found")
)
