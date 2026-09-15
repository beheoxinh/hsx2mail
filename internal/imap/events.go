// Package imap provides IMAP client functionality
package imap

// EventType represents the type of mail event
type EventType int

const (
	// EventNewMail indicates new messages have arrived
	EventNewMail EventType = iota
	// EventExpunge indicates messages have been deleted
	EventExpunge
	// EventFlagsChanged indicates message flags have changed
	EventFlagsChanged
)

// MailEvent represents a mailbox change notification
type MailEvent struct {
	Type      EventType
	AccountID string
	FolderID  string // Database folder ID (if known)
	Folder    string // IMAP folder path (e.g., "INBOX")
	Count     uint32 // Number of messages (for EXISTS)
	SeqNum    uint32 // Sequence number (for EXPUNGE/FLAGS)
}

// String returns a string representation of the event type
func (t EventType) String() string {
	switch t {
	case EventNewMail:
		return "NewMail"
	case EventExpunge:
		return "Expunge"
	case EventFlagsChanged:
		return "FlagsChanged"
	default:
		return "Unknown"
	}
}
