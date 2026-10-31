// Package mail is the provider-independent email boundary for certificate
// delivery (M5b). The delivery service depends only on Mailer; SMTP is one
// implementation and Fake is another for tests. Errors are classified as
// transient (retry later) or permanent (give up) and carry only a short,
// PII-free code: provider response text may echo recipient addresses, so it
// is never stored or logged.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"sync"
	"time"
)

// MaxAttachmentBytes bounds the PDF attached to a message. Most providers
// reject messages above ~10 MB once base64-encoded.
const MaxAttachmentBytes = 5 << 20

// Attachment is a file sent with a message.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message is one outgoing email.
type Message struct {
	From        string // RFC 5322 address, e.g. "Certificates <no-reply@example.com>"
	To          string // a single recipient address
	Subject     string
	Text        string
	Attachments []Attachment
	// MessageID is optional; BuildMIME generates one when empty.
	MessageID string
}

// Mailer sends a message. A nil error means the provider ACCEPTED the message
// for delivery, not that it reached the inbox.
type Mailer interface {
	Send(ctx context.Context, m Message) error
	// Name identifies the provider in attempt records ("smtp", "fake").
	Name() string
}

// Error is a classified send failure. Code is a short stable identifier safe
// to store and show (never provider text).
type Error struct {
	Transient bool
	Code      string
	Err       error // underlying cause, for logs only; may contain addresses, never persisted
}

func (e *Error) Error() string { return "mail: " + e.Code }
func (e *Error) Unwrap() error { return e.Err }

// Classify reports whether err is transient and its code. Unclassified errors
// (including context errors) are treated as transient "unknown".
func Classify(err error) (transient bool, code string) {
	var me *Error
	if errors.As(err, &me) {
		return me.Transient, me.Code
	}
	return true, "unknown"
}

// Stable error codes.
const (
	CodeConnect       = "connect_failed" // transient
	CodeTimeout       = "timeout"        // transient
	CodeTLS           = "tls_failed"     // permanent: misconfiguration, retrying will not help
	CodeAuth          = "auth_failed"    // permanent
	CodeTemporary     = "provider_temporary"
	CodeRejected      = "provider_rejected" // permanent (5xx)
	CodeRecipient     = "recipient_rejected"
	CodeInvalidMsg    = "invalid_message"
	CodeNotConfigured = "not_configured"
)

// ValidateAddress checks a single address and returns its bare form. It
// rejects control characters (header injection), display names and lists.
func ValidateAddress(s string) (string, error) {
	if s == "" || len(s) > 254 || strings.ContainsAny(s, "\r\n\x00") {
		return "", errors.New("invalid address")
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address == "" || a.Name != "" || a.Address != s {
		return "", errors.New("invalid address")
	}
	at := strings.LastIndex(a.Address, "@")
	if at < 1 || !strings.Contains(a.Address[at+1:], ".") {
		return "", errors.New("invalid address")
	}
	return a.Address, nil
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return ' '
		}
		return r
	}, s)
}

// BuildMIME renders m as an RFC 5322 message (multipart/mixed with a UTF-8
// text part and base64 attachments). Header values are sanitised so no field
// can inject extra headers.
func BuildMIME(m Message, now time.Time) ([]byte, error) {
	from, err := mail.ParseAddress(clean(m.From))
	if err != nil || strings.ContainsAny(m.From, "\r\n") {
		return nil, &Error{Code: CodeInvalidMsg, Err: errors.New("bad sender")}
	}
	to, err := ValidateAddress(m.To)
	if err != nil {
		return nil, &Error{Code: CodeRecipient, Err: err}
	}
	total := 0
	for _, a := range m.Attachments {
		total += len(a.Data)
	}
	if total > MaxAttachmentBytes {
		return nil, &Error{Code: CodeInvalidMsg, Err: errors.New("attachment too large")}
	}
	id := m.MessageID
	if id == "" {
		var b [12]byte
		_, _ = rand.Read(b[:])
		domain := from.Address[strings.LastIndex(from.Address, "@")+1:]
		id = hex.EncodeToString(b[:]) + "@" + domain
	}
	var bd [12]byte
	_, _ = rand.Read(bd[:])
	boundary := "cos-" + hex.EncodeToString(bd[:])

	var buf bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	h("From", from.String())
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", clean(m.Subject)))
	h("Date", now.UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+clean(id)+">")
	h("MIME-Version", "1.0")
	h("Auto-Submitted", "auto-generated")
	h("Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	buf.WriteString("\r\n")

	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	writeB64(&buf, []byte(m.Text))
	for _, a := range m.Attachments {
		name := mime.QEncoding.Encode("utf-8", clean(a.Filename))
		ct := clean(a.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s; name=%q\r\n", ct, name)
		fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=%q\r\n", name)
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		writeB64(&buf, a.Data)
	}
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return buf.Bytes(), nil
}

func writeB64(buf *bytes.Buffer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		buf.WriteString(enc[:76])
		buf.WriteString("\r\n")
		enc = enc[76:]
	}
	buf.WriteString(enc)
	buf.WriteString("\r\n")
}

// Fake is an in-memory Mailer for tests: it records accepted messages and can
// be scripted to fail.
type Fake struct {
	mu       sync.Mutex
	sent     []Message
	failures []error // consumed one per Send call, in order; nil entries succeed
	calls    int
}

func NewFake() *Fake { return &Fake{} }

func (f *Fake) Name() string { return "fake" }

// FailNext queues errors returned by the next Send calls (nil = succeed).
func (f *Fake) FailNext(errs ...error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, errs...)
}

func (f *Fake) Send(_ context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.failures) > 0 {
		err := f.failures[0]
		f.failures = f.failures[1:]
		if err != nil {
			return err
		}
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *Fake) Sent() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.sent...)
}

func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func parseAddr(s string) (string, error) {
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address == "" || strings.ContainsAny(a.Address, "\r\n") {
		return "", errors.New("invalid sender")
	}
	return a.Address, nil
}
