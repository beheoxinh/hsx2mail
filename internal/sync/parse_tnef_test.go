package sync

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	gomessage "github.com/emersion/go-message"
	"github.com/rs/zerolog"
)

// --- minimal TNEF + MIME builders (self-contained for this package) ---

func tnefObj(level, name int, data []byte) []byte {
	var b bytes.Buffer
	b.WriteByte(byte(level))
	_ = binary.Write(&b, binary.LittleEndian, uint16(name))
	_ = binary.Write(&b, binary.LittleEndian, uint16(0))
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	_ = binary.Write(&b, binary.LittleEndian, uint16(0))
	return b.Bytes()
}

func buildWinmailDat(filename string, content []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint32(0x223e9f78))
	_ = binary.Write(&b, binary.LittleEndian, uint16(0))
	b.Write(tnefObj(0x02, 0x9002, []byte{0}))
	b.Write(tnefObj(0x02, 0x8010, append([]byte(filename), 0)))
	b.Write(tnefObj(0x02, 0x800f, content))
	return b.Bytes()
}

func b64wrap(b []byte) string {
	enc := base64.StdEncoding.EncodeToString(b)
	var sb strings.Builder
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		sb.WriteString(enc[i:end])
		sb.WriteString("\r\n")
	}
	return sb.String()
}

func parseAttachments(t *testing.T, raw []byte) *ParsedBody {
	t.Helper()
	entity, err := gomessage.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gomessage.Read: %v", err)
	}
	mr := entity.MultipartReader()
	if mr == nil {
		t.Fatal("expected a multipart message")
	}
	e := &Engine{log: zerolog.Nop()}
	result := &ParsedBody{}
	e.parseMultipartBody(mr, result, "msg-1")
	return result
}

// TestParseMultipartBody_TNEF proves the live sync extractor surfaces the inner
// attachment of a winmail.dat container by its real name — not "winmail.dat".
func TestParseMultipartBody_TNEF(t *testing.T) {
	tnefBytes := buildWinmailDat("report.pdf", []byte("inner pdf bytes"))

	var sb strings.Builder
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: multipart/mixed; boundary=\"BOUND\"\r\n\r\n")
	sb.WriteString("--BOUND\r\n")
	sb.WriteString("Content-Type: text/plain\r\n\r\nhello\r\n")
	sb.WriteString("--BOUND\r\n")
	sb.WriteString("Content-Type: application/ms-tnef; name=\"winmail.dat\"\r\n")
	sb.WriteString("Content-Transfer-Encoding: base64\r\n")
	sb.WriteString("Content-Disposition: attachment; filename=\"winmail.dat\"\r\n\r\n")
	sb.WriteString(b64wrap(tnefBytes))
	sb.WriteString("\r\n--BOUND--\r\n")

	result := parseAttachments(t, []byte(sb.String()))

	if len(result.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d: %+v", len(result.Attachments), result.Attachments)
	}
	if got := result.Attachments[0].Filename; got != "report.pdf" {
		t.Errorf("attachment filename = %q, want report.pdf (TNEF inner file, not the container)", got)
	}
	if !result.HasAttachments {
		t.Error("HasAttachments = false, want true")
	}
}

// TestParseMultipartBody_Regular confirms the TNEF branch doesn't disturb normal
// messages: a regular attachment + an inline image are both still captured.
func TestParseMultipartBody_Regular(t *testing.T) {
	pdf := []byte("%PDF regular doc")
	png := []byte("\x89PNG fake")

	var sb strings.Builder
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: multipart/mixed; boundary=\"OUT\"\r\n\r\n")
	sb.WriteString("--OUT\r\n")
	sb.WriteString("Content-Type: text/plain\r\n\r\nbody text\r\n")
	sb.WriteString("--OUT\r\n")
	sb.WriteString("Content-Type: application/pdf; name=\"doc.pdf\"\r\n")
	sb.WriteString("Content-Transfer-Encoding: base64\r\n")
	sb.WriteString("Content-Disposition: attachment; filename=\"doc.pdf\"\r\n\r\n")
	sb.WriteString(b64wrap(pdf))
	sb.WriteString("\r\n--OUT\r\n")
	sb.WriteString("Content-Type: image/png\r\n")
	sb.WriteString("Content-Transfer-Encoding: base64\r\n")
	sb.WriteString("Content-ID: <logo>\r\n\r\n")
	sb.WriteString(b64wrap(png))
	sb.WriteString("\r\n--OUT--\r\n")

	result := parseAttachments(t, []byte(sb.String()))

	if len(result.Attachments) != 2 {
		t.Fatalf("expected 2 attachments (pdf + inline png), got %d: %+v", len(result.Attachments), result.Attachments)
	}
	var names []string
	for _, a := range result.Attachments {
		names = append(names, a.Filename)
	}
	if !contains(names, "doc.pdf") {
		t.Errorf("missing doc.pdf, got %v", names)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
