package smtp

import (
	"strings"
	"testing"
)

// TestSanitizeHeaderValue covers 5-07: no outbound header value may carry a
// CR, LF or NUL.
func TestSanitizeHeaderValue(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"a\r\nB: evil", "a  B: evil"},
		{"a\nb", "a b"},
		{"a\rb", "a b"},
		{"a\x00b", "ab"},
		{"line1\r\n\r\nX-Injected: 1", "line1    X-Injected: 1"},
	}
	for _, tc := range tests {
		if got := sanitizeHeaderValue(tc.in); got != tc.want {
			t.Errorf("sanitizeHeaderValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSanitizeMIMEType proves an injected Content-Type cannot survive.
func TestSanitizeMIMEType(t *testing.T) {
	const fallback = "application/octet-stream"

	tests := []struct{ name, in, want string }{
		{"plain", "image/png", "image/png"},
		{"with charset", "text/plain; charset=utf-8", "text/plain; charset=utf-8"},
		{"case normalized", "IMAGE/PNG", "image/png"},
		{"crlf injection", "text/plain\r\nX-Injected: 1", fallback},
		{"cr only", "text/plain\rX: 1", fallback},
		{"no slash", "evil", fallback},
		{"empty", "", fallback},
		{"bad param dropped", "text/plain; \r\nX: 1", "text/plain"},
		{"quoted param dropped", `text/plain; name="a`, "text/plain"},
		{"null byte stripped", "text/pl\x00ain", "text/plain"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeMIMEType(tc.in, fallback)
			if got != tc.want {
				t.Fatalf("sanitizeMIMEType(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsAny(got, "\r\n\x00") {
				t.Fatalf("result still carries a control character: %q", got)
			}
		})
	}
}

// TestSanitizeContentID covers the angle-bracket breakout.
func TestSanitizeContentID(t *testing.T) {
	tests := []struct{ in, want string }{
		{"img001", "img001"},
		{"img@1", "img@1"},
		{"a>\r\nX-Injected: 1", "aX-Injected:1"},
		{"<script>", "script"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := sanitizeContentID(tc.in); got != tc.want {
			t.Errorf("sanitizeContentID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestToRFC822NeutralizesHeaderInjection is the end-to-end check: every field
// a caller controls must come out of the builder without an injectable CRLF.
func TestToRFC822NeutralizesHeaderInjection(t *testing.T) {
	msg := ComposeMessage{
		From:       Address{Name: "Evil\r\nBcc: attacker@evil.test", Address: "me@test\r\nX: 1"},
		To:         []Address{{Name: "Bob\r\nX-Injected: 1", Address: "bob@test\r\nX: 2"}},
		Cc:         []Address{{Address: "carol@test"}},
		Bcc:        []Address{{Address: "dave@test"}},
		Subject:    "Hello\r\nX-Injected: yes",
		TextBody:   "body\r\ntext",
		InReplyTo:  "<parent@test>\r\nX-Injected: 2",
		References: []string{"<root@test>\r\nX-Injected: 3"},
		Attachments: []Attachment{{
			Filename:    `evil".pdf` + "\r\nX-Injected: 4",
			ContentType: "application/pdf\r\nX-Injected: 5",
			ContentID:   "cid1>\r\nX-Injected: 6",
			Content:     []byte("data"),
		}},
	}

	msg.Sanitize()

	raw, err := msg.ToRFC822()
	if err != nil {
		t.Fatalf("ToRFC822: %v", err)
	}
	out := string(raw)

	header, _, ok := strings.Cut(out, "\r\n\r\n")
	if !ok {
		t.Fatal("no header/body split found")
	}

	// No line may be a header the attacker asked for. A flattened value such as
	// "Evil Bcc: attacker@evil.test" is harmless — it stays inside the From
	// display name — so assert on line starts, not substrings.
	for _, injected := range []string{
		"bcc:", "x-injected:", "bcc:attacker",
	} {
		for _, line := range strings.Split(header, "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), injected) {
				t.Errorf("injected header line %q survived:\n%s", line, header)
			}
		}
	}
	// Unfolding must be impossible: no line may contain a bare CR or LF.
	if strings.ContainsAny(header, "\n") && strings.Count(header, "\n") != strings.Count(header, "\r\n") {
		t.Errorf("header block contains a bare LF:\n%q", header)
	}

	// Every header line must still be a well-formed `Name: value` pair.
	for _, line := range strings.Split(header, "\r\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(line, ":") {
			t.Errorf("malformed header line: %q", line)
		}
	}
}

// TestComposeMessageSanitizePreservesLegitimateValues guards against the
// sanitizer mangling normal input.
func TestComposeMessageSanitizePreservesLegitimateValues(t *testing.T) {
	msg := ComposeMessage{
		From:       Address{Name: "Ada Lovelace", Address: "ada@example.test"},
		To:         []Address{{Name: "Grace Hopper", Address: "grace+tag@example.test"}},
		Subject:    "Re: quarterly numbers",
		InReplyTo:  "<abc@example.test>",
		References: []string{"<root@example.test>", "<abc@example.test>"},
	}
	before := msg
	msg.Sanitize()

	if msg.From != before.From {
		t.Errorf("From mangled: %+v", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0] != before.To[0] {
		t.Errorf("To mangled: %+v", msg.To)
	}
	if msg.Subject != before.Subject {
		t.Errorf("Subject mangled: %q", msg.Subject)
	}
	if msg.InReplyTo != before.InReplyTo {
		t.Errorf("InReplyTo mangled: %q", msg.InReplyTo)
	}
	if len(msg.References) != 2 || msg.References[1] != before.References[1] {
		t.Errorf("References mangled: %+v", msg.References)
	}
	// Bodies must survive untouched.
	msg2 := ComposeMessage{TextBody: "line1\nline2"}
	msg2.Sanitize()
	if msg2.TextBody != "line1\nline2" {
		t.Errorf("body mangled: %q", msg2.TextBody)
	}
}
