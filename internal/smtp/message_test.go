package smtp

import (
	"strings"
	"testing"
)

func TestAddressString_WithName(t *testing.T) {
	addr := Address{Name: "John Doe", Address: "john@example.com"}
	result := addr.String()

	if !strings.Contains(result, "John Doe") {
		t.Errorf("expected result to contain 'John Doe', got %q", result)
	}
	if !strings.Contains(result, "john@example.com") {
		t.Errorf("expected result to contain 'john@example.com', got %q", result)
	}
}

func TestAddressString_WithoutName(t *testing.T) {
	addr := Address{Address: "john@example.com"}
	result := addr.String()

	if result != "john@example.com" {
		t.Errorf("expected 'john@example.com', got %q", result)
	}
}

func TestAddressString_Unicode(t *testing.T) {
	addr := Address{Name: "\u65e5\u672c\u8a9e", Address: "test@example.com"}
	result := addr.String()

	if !strings.Contains(result, "test@example.com") {
		t.Errorf("expected result to contain 'test@example.com', got %q", result)
	}
	// The name should be Q-encoded for non-ASCII characters
	if !strings.Contains(result, "=?utf-8?") {
		t.Errorf("expected result to contain encoded name, got %q", result)
	}
}

func TestAllRecipients(t *testing.T) {
	msg := &ComposeMessage{
		To:  []Address{{Address: "to1@example.com"}, {Address: "to2@example.com"}},
		Cc:  []Address{{Address: "cc1@example.com"}},
		Bcc: []Address{{Address: "bcc1@example.com"}},
	}

	recipients := msg.AllRecipients()
	if len(recipients) != 4 {
		t.Fatalf("AllRecipients() returned %d recipients, want 4", len(recipients))
	}

	expected := []string{"to1@example.com", "to2@example.com", "cc1@example.com", "bcc1@example.com"}
	for i, want := range expected {
		if recipients[i] != want {
			t.Errorf("AllRecipients()[%d] = %q, want %q", i, recipients[i], want)
		}
	}
}

func TestAllRecipients_Empty(t *testing.T) {
	msg := &ComposeMessage{}
	recipients := msg.AllRecipients()

	if recipients != nil {
		t.Errorf("AllRecipients() = %v, want nil", recipients)
	}
}

func TestToRFC822_Basic(t *testing.T) {
	msg := &ComposeMessage{
		From:     Address{Name: "Sender", Address: "sender@example.com"},
		To:       []Address{{Name: "Recipient", Address: "recipient@example.com"}},
		Subject:  "Test Subject",
		TextBody: "Hello, this is a test.",
	}

	data, err := msg.ToRFC822()
	if err != nil {
		t.Fatalf("ToRFC822() returned error: %v", err)
	}

	output := string(data)

	checks := []struct {
		label    string
		contains string
	}{
		{"From header", "From:"},
		{"To header", "To:"},
		{"Subject header", "Subject:"},
		{"MIME-Version", "MIME-Version: 1.0"},
		{"text body", "Hello, this is a test"},
	}

	for _, check := range checks {
		if !strings.Contains(output, check.contains) {
			t.Errorf("expected RFC822 output to contain %s (%q), but it was missing", check.label, check.contains)
		}
	}
}
