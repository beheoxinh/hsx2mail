// Package smtp provides SMTP client functionality
package smtp

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/message"
)

// MDNDisposition represents the disposition type for an MDN
type MDNDisposition string

const (
	// MDNDisplayed indicates the message was displayed to the user
	MDNDisplayed MDNDisposition = "displayed"
	// MDNDeleted indicates the message was deleted without being displayed
	MDNDeleted MDNDisposition = "deleted"
)

// BuildMDN creates an RFC 3798 compliant Message Disposition Notification
// Parameters:
//   - originalMsg: The original message that requested the receipt
//   - fromName: The name of the sender (current user)
//   - fromEmail: The email of the sender (current user)
//   - disposition: The disposition type (displayed, deleted, etc.)
//
// Returns the complete MDN message as bytes ready to be sent via SMTP
func BuildMDN(originalMsg *message.Message, fromName, fromEmail string, disposition MDNDisposition) ([]byte, error) {
	if originalMsg == nil {
		return nil, fmt.Errorf("original message is required")
	}
	if originalMsg.ReadReceiptTo == "" {
		return nil, fmt.Errorf("original message has no read receipt request")
	}
	if fromEmail == "" {
		return nil, fmt.Errorf("from email is required")
	}

	// Generate a unique message ID
	msgID := fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), "mdn", domainFromEmail(fromEmail))

	// Build the recipient (who requested the receipt)
	recipientEmail := extractEmailAddress(originalMsg.ReadReceiptTo)

	// Generate boundary for multipart message
	boundary := fmt.Sprintf("=_mdn_%d", time.Now().UnixNano())

	var buf bytes.Buffer

	// Every value below comes from a received message, so it is attacker
	// controlled. These headers are written as raw bytes, not via a MIME
	// encoder, so a CR/LF in the source would let a sender inject arbitrary
	// headers (or a whole body) into the receipt we send back.
	safeTo := stripCRLF(originalMsg.ReadReceiptTo)
	safeSubject := stripCRLF(originalMsg.Subject)
	safeOriginalID := stripCRLF(originalMsg.MessageID)
	safeFromEmail := stripCRLF(fromEmail)
	safeFromName := stripCRLF(fromName)
	safeRecipient := stripCRLF(recipientEmail)

	// Write headers
	buf.WriteString(fmt.Sprintf("From: %s\r\n", formatAddress(safeFromName, safeFromEmail)))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", safeTo))
	buf.WriteString(fmt.Sprintf("Subject: Read: %s\r\n", safeSubject))
	buf.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	buf.WriteString(fmt.Sprintf("Message-ID: %s\r\n", msgID))
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/report; report-type=disposition-notification; boundary=\"%s\"\r\n", boundary))
	buf.WriteString("\r\n")

	// Write human-readable part
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString("Your message\r\n\r\n")
	buf.WriteString(fmt.Sprintf("  To: %s\r\n", safeFromEmail))
	buf.WriteString(fmt.Sprintf("  Subject: %s\r\n", safeSubject))
	buf.WriteString(fmt.Sprintf("  Sent: %s\r\n", originalMsg.Date.Format(time.RFC1123Z)))
	buf.WriteString("\r\n")
	buf.WriteString(fmt.Sprintf("was %s on %s.\r\n", stripCRLF(string(disposition)), time.Now().Format(time.RFC1123Z)))
	buf.WriteString("\r\n")

	// Write machine-readable disposition notification part
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: message/disposition-notification\r\n")
	buf.WriteString("\r\n")
	buf.WriteString("Reporting-UA: Hsx2Mail/1.0\r\n")
	if originalMsg.MessageID != "" {
		buf.WriteString(fmt.Sprintf("Original-Message-ID: %s\r\n", safeOriginalID))
	}
	buf.WriteString(fmt.Sprintf("Final-Recipient: rfc822; %s\r\n", safeFromEmail))
	buf.WriteString(fmt.Sprintf("Original-Recipient: rfc822; %s\r\n", safeRecipient))
	buf.WriteString(fmt.Sprintf("Disposition: manual-action/MDN-sent-manually; %s\r\n", stripCRLF(string(disposition))))
	buf.WriteString("\r\n")

	// Close multipart
	buf.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	return buf.Bytes(), nil
}

// domainFromEmail extracts the domain part from an email address
func domainFromEmail(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return "localhost"
}

// extractEmailAddress extracts just the email address from a potentially formatted address
// e.g., "John Doe <john@example.com>" -> "john@example.com"
func extractEmailAddress(addr string) string {
	addr = strings.TrimSpace(addr)

	// Check if it's in "Name <email>" format
	if start := strings.Index(addr, "<"); start != -1 {
		if end := strings.Index(addr, ">"); end > start {
			return addr[start+1 : end]
		}
	}

	// Otherwise, assume it's just an email address
	return addr
}

// formatAddress formats a name and email into a proper address string
func formatAddress(name, email string) string {
	if name == "" {
		return email
	}
	// Check if name needs quoting (contains special characters)
	if strings.ContainsAny(name, `"(),.:;<>@[\]`) {
		return fmt.Sprintf(`"%s" <%s>`, strings.ReplaceAll(name, `"`, `\"`), email)
	}
	return fmt.Sprintf("%s <%s>", name, email)
}

// stripCRLF removes CR and LF so a value taken from a received message cannot
// terminate the header it is being written into. Delegates to the shared header
// sanitizer so MDN, the message builder, and attachments all agree.
func stripCRLF(s string) string {
	return sanitizeHeaderValue(s)
}
