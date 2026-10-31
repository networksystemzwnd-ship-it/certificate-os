package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"time"
)

// TLS modes for SMTP.
const (
	TLSStartTLS = "starttls" // connect in clear, upgrade with STARTTLS (port 587); refuses to continue if unavailable
	TLSImplicit = "tls"      // TLS from the first byte (port 465)
	TLSNone     = "none"     // local development only (e.g. Mailpit)
)

// SMTP sends through an SMTP server using only the standard library.
// Credentials are used for PLAIN auth and are never logged or returned in errors.
type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	Mode     string
	// TLSConfig overrides the default (ServerName = Host); used by tests.
	TLSConfig *tls.Config
	// Timeout bounds the whole exchange (default 30s).
	Timeout time.Duration
	Now     func() time.Time
}

func (s *SMTP) Name() string { return "smtp" }

func (s *SMTP) tlsConfig() *tls.Config {
	if s.TLSConfig != nil {
		c := s.TLSConfig.Clone()
		if c.ServerName == "" {
			c.ServerName = s.Host
		}
		return c
	}
	return &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	body, err := BuildMIME(m, now())
	if err != nil {
		return err
	}
	to, _ := ValidateAddress(m.To) // BuildMIME already validated
	from, err := parseSender(m.From)
	if err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()

	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	d := &net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return wrapNet(err, CodeConnect)
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)
	if s.Mode == TLSImplicit {
		tc := tls.Client(conn, s.tlsConfig())
		if err := tc.HandshakeContext(ctx); err != nil {
			return &Error{Code: CodeTLS, Err: err}
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return classifySMTP(err, CodeConnect)
	}
	defer c.Close()
	if s.Mode == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return &Error{Code: CodeTLS, Err: errors.New("server does not offer STARTTLS")}
		}
		if err := c.StartTLS(s.tlsConfig()); err != nil {
			return &Error{Code: CodeTLS, Err: err}
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return classifyAuth(err)
		}
	}
	if err := c.Mail(from); err != nil {
		return classifySMTP(err, CodeRejected)
	}
	if err := c.Rcpt(to); err != nil {
		return classifySMTP(err, CodeRecipient)
	}
	w, err := c.Data()
	if err != nil {
		return classifySMTP(err, CodeRejected)
	}
	if _, err := w.Write(body); err != nil {
		return wrapNet(err, CodeConnect)
	}
	// The server's reply to the final "." is what accepts the message.
	if err := w.Close(); err != nil {
		return classifySMTP(err, CodeRejected)
	}
	_ = c.Quit()
	return nil
}

func parseSender(from string) (string, error) {
	// BuildMIME validated the display form; the envelope needs the bare address.
	a, err := parseAddr(from)
	if err != nil {
		return "", &Error{Code: CodeInvalidMsg, Err: err}
	}
	return a, nil
}

func wrapNet(err error, code string) error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return &Error{Transient: true, Code: CodeTimeout, Err: err}
	}
	return &Error{Transient: true, Code: code, Err: err}
}

// classifySMTP maps an SMTP reply: 4xx is transient, 5xx permanent. Anything
// that is not a protocol reply is a transient network problem.
func classifySMTP(err error, permanentCode string) error {
	var te *textproto.Error
	if errors.As(err, &te) {
		if te.Code >= 400 && te.Code < 500 {
			return &Error{Transient: true, Code: CodeTemporary, Err: err}
		}
		return &Error{Code: permanentCode, Err: err}
	}
	return wrapNet(err, CodeConnect)
}

func classifyAuth(err error) error {
	var te *textproto.Error
	if errors.As(err, &te) && te.Code >= 400 && te.Code < 500 {
		return &Error{Transient: true, Code: CodeTemporary, Err: err}
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return wrapNet(err, CodeConnect)
	}
	return &Error{Code: CodeAuth, Err: err}
}
