package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net"
	stdmail "net/mail"
	"strings"
	"sync"
	"testing"
	"time"
)

type srvOpts struct {
	cert       *tls.Certificate
	implicit   bool
	offerTLS   bool
	wantAuth   bool
	user, pass string
	mailReply  string
	rcptReply  string
	dataReply  string
	hang       bool
}

type fakeSMTP struct {
	ln   net.Listener
	opts srvOpts
	mu   sync.Mutex
	got  []string // raw DATA bodies
	from []string
	rcpt []string
	auth []string
	tls  []bool
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func startSMTP(t *testing.T, o srvOpts) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if o.implicit {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{*o.cert}})
	}
	s := &fakeSMTP{ln: ln, opts: o}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *fakeSMTP) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	o := s.opts
	isTLS := o.implicit
	r := bufio.NewReader(c)
	w := func(line string) { fmt.Fprintf(c, "%s\r\n", line) }
	w("220 fake ESMTP")
	if o.hang {
		time.Sleep(5 * time.Second)
		return
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			w("250-fake")
			if o.offerTLS && !isTLS {
				w("250-STARTTLS")
			}
			if o.wantAuth {
				w("250-AUTH PLAIN")
			}
			w("250 8BITMIME")
		case up == "STARTTLS":
			w("220 go ahead")
			tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{*o.cert}})
			if err := tc.Handshake(); err != nil {
				return
			}
			c = tc
			r = bufio.NewReader(tc)
			w = func(line string) { fmt.Fprintf(tc, "%s\r\n", line) }
			isTLS = true
		case strings.HasPrefix(up, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")
			s.mu.Lock()
			s.auth = append(s.auth, string(raw))
			s.mu.Unlock()
			if len(parts) == 3 && parts[1] == o.user && parts[2] == o.pass {
				w("235 ok")
			} else {
				w("535 5.7.8 bad credentials")
			}
		case strings.HasPrefix(up, "MAIL FROM:"):
			if o.mailReply != "" {
				w(o.mailReply)
				continue
			}
			s.mu.Lock()
			s.from = append(s.from, line)
			s.tls = append(s.tls, isTLS)
			s.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(up, "RCPT TO:"):
			if o.rcptReply != "" {
				w(o.rcptReply)
				continue
			}
			s.mu.Lock()
			s.rcpt = append(s.rcpt, line)
			s.mu.Unlock()
			w("250 ok")
		case up == "DATA":
			w("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			if o.dataReply != "" {
				w(o.dataReply)
				continue
			}
			s.mu.Lock()
			s.got = append(s.got, b.String())
			s.mu.Unlock()
			w("250 2.0.0 queued")
		case up == "QUIT":
			w("221 bye")
			return
		default:
			w("502 unknown")
		}
	}
}

func testMsg() Message {
	return Message{
		From: "Certificates <no-reply@example.com>", To: "asha@example.com",
		Subject: "Your certificate\r\nBcc: evil@example.com", Text: "Hello Asha — your certificate is attached.",
		Attachments: []Attachment{{Filename: "CERT-2026-000001.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.4 fake")}},
	}
}

func client(s *fakeSMTP, mode string, pool *x509.CertPool) *SMTP {
	c := &SMTP{Host: "127.0.0.1", Port: s.port(), Mode: mode, Timeout: 3 * time.Second}
	if pool != nil {
		c.TLSConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return c
}

func code(err error) (bool, string) { return Classify(err) }

func TestSMTPPlainDeliversMIMERoundTrip(t *testing.T) {
	s := startSMTP(t, srvOpts{})
	if err := client(s, TLSNone, nil).Send(context.Background(), testMsg()); err != nil {
		t.Fatal(err)
	}
	if len(s.got) != 1 || len(s.rcpt) != 1 || !strings.Contains(s.rcpt[0], "<asha@example.com>") || !strings.Contains(s.from[0], "<no-reply@example.com>") {
		t.Fatalf("envelope wrong: %v %v %v", s.from, s.rcpt, len(s.got))
	}
	msg, err := stdmail.ReadMessage(strings.NewReader(s.got[0]))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Fatal("header injection through Subject")
	}
	if len(msg.Header["To"]) != 1 || msg.Header.Get("To") != "asha@example.com" {
		t.Fatalf("To: %v", msg.Header["To"])
	}
	if !strings.HasPrefix(msg.Header.Get("Content-Type"), "multipart/mixed") {
		t.Fatal(msg.Header.Get("Content-Type"))
	}
	body, _ := io.ReadAll(msg.Body)
	// the text part and the attachment are base64 encoded: find both payloads
	if !strings.Contains(string(body), base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 fake"))) {
		t.Fatal("attachment missing")
	}
	if !strings.Contains(string(body), "filename=") {
		t.Fatal("attachment name missing")
	}
}

func TestSMTPStartTLSAndAuth(t *testing.T) {
	cert, pool := selfSigned(t)
	s := startSMTP(t, srvOpts{cert: &cert, offerTLS: true, wantAuth: true, user: "u", pass: "secret"})
	c := client(s, TLSStartTLS, pool)
	c.Username, c.Password = "u", "secret"
	if err := c.Send(context.Background(), testMsg()); err != nil {
		t.Fatal(err)
	}
	if len(s.tls) != 1 || !s.tls[0] {
		t.Fatal("message was not sent over TLS")
	}
	if len(s.auth) != 1 {
		t.Fatal("auth not used")
	}
}

func TestSMTPImplicitTLS(t *testing.T) {
	cert, pool := selfSigned(t)
	s := startSMTP(t, srvOpts{cert: &cert, implicit: true})
	if err := client(s, TLSImplicit, pool).Send(context.Background(), testMsg()); err != nil {
		t.Fatal(err)
	}
	if len(s.got) != 1 {
		t.Fatal("not delivered")
	}
}

func TestSMTPStartTLSRequiredNeverFallsBackToClear(t *testing.T) {
	s := startSMTP(t, srvOpts{offerTLS: false})
	err := client(s, TLSStartTLS, nil).Send(context.Background(), testMsg())
	tr, c := code(err)
	if err == nil || tr || c != CodeTLS {
		t.Fatalf("err=%v transient=%v code=%s", err, tr, c)
	}
	if len(s.got) != 0 || len(s.rcpt) != 0 {
		t.Fatal("a message was sent without TLS")
	}
}

func TestSMTPUntrustedCertificateRejected(t *testing.T) {
	cert, _ := selfSigned(t)
	s := startSMTP(t, srvOpts{cert: &cert, offerTLS: true})
	err := client(s, TLSStartTLS, x509.NewCertPool()).Send(context.Background(), testMsg())
	if tr, c := code(err); err == nil || tr || c != CodeTLS {
		t.Fatalf("err=%v transient=%v code=%s", err, tr, c)
	}
	if len(s.got) != 0 {
		t.Fatal("sent over unverified TLS")
	}
}

func TestSMTPErrorClassification(t *testing.T) {
	cases := []struct {
		name      string
		o         srvOpts
		user      string
		transient bool
		code      string
	}{
		{"temporary 451 at data", srvOpts{dataReply: "451 4.3.0 try later"}, "", true, CodeTemporary},
		{"temporary 421 at rcpt", srvOpts{rcptReply: "421 busy"}, "", true, CodeTemporary},
		{"recipient 550", srvOpts{rcptReply: "550 5.1.1 no such user asha@example.com"}, "", false, CodeRecipient},
		{"sender refused 553", srvOpts{mailReply: "553 sender not allowed"}, "", false, CodeRejected},
		{"data rejected 554", srvOpts{dataReply: "554 spam"}, "", false, CodeRejected},
		{"bad credentials", srvOpts{wantAuth: true, user: "u", pass: "right"}, "u", false, CodeAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := startSMTP(t, tc.o)
			c := client(s, TLSNone, nil)
			if tc.user != "" {
				c.Username, c.Password = tc.user, "wrong"
			}
			err := c.Send(context.Background(), testMsg())
			tr, cd := code(err)
			if err == nil || tr != tc.transient || cd != tc.code {
				t.Fatalf("err=%v transient=%v code=%s", err, tr, cd)
			}
			// The recorded error text must not carry provider text or addresses.
			if strings.Contains(err.Error(), "example.com") {
				t.Fatalf("error text leaks the recipient: %s", err.Error())
			}
		})
	}
}

func TestSMTPConnectionRefusedAndTimeoutAreTransient(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	c := &SMTP{Host: "127.0.0.1", Port: port, Mode: TLSNone, Timeout: time.Second}
	err := c.Send(context.Background(), testMsg())
	if tr, cd := code(err); err == nil || !tr || cd != CodeConnect {
		t.Fatalf("refused: %v %v %s", err, tr, cd)
	}
	s := startSMTP(t, srvOpts{hang: true})
	c2 := client(s, TLSNone, nil)
	c2.Timeout = 300 * time.Millisecond
	err = c2.Send(context.Background(), testMsg())
	if tr, cd := code(err); err == nil || !tr || (cd != CodeTimeout && cd != CodeConnect) {
		t.Fatalf("timeout: %v %v %s", err, tr, cd)
	}
}

func TestBuildMIMERejectsBadInput(t *testing.T) {
	bad := []Message{
		{From: "a@example.com", To: "x@example.com\r\nBcc: y@example.com", Subject: "s"},
		{From: "a@example.com", To: "x@example.com, y@example.com", Subject: "s"},
		{From: "a@example.com", To: "Name <x@example.com>", Subject: "s"},
		{From: "a@example.com", To: "", Subject: "s"},
		{From: "not an address", To: "x@example.com", Subject: "s"},
		{From: "a@example.com", To: "x@example.com", Subject: "s", Attachments: []Attachment{{Data: make([]byte, MaxAttachmentBytes+1)}}},
	}
	for i, m := range bad {
		if _, err := BuildMIME(m, time.Now()); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if _, err := ValidateAddress("a@b"); err == nil {
		t.Error("domain without dot accepted")
	}
}

func TestFakeMailer(t *testing.T) {
	f := NewFake()
	f.FailNext(&Error{Transient: true, Code: CodeConnect}, nil)
	if err := f.Send(context.Background(), testMsg()); err == nil {
		t.Fatal("scripted failure missing")
	}
	if err := f.Send(context.Background(), testMsg()); err != nil || len(f.Sent()) != 1 || f.Calls() != 2 {
		t.Fatal("fake accounting wrong")
	}
}
