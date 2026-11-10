// Package smtptest is an in-process SMTP server for tests. It accepts
// messages and keeps them; nothing leaves the machine.
package smtptest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Message is one accepted message.
type Message struct {
	From string
	To   []string
	Data string
	// TLS and Auth say how the session that sent it was protected.
	TLS  bool
	Auth string // the user name that signed in, "" if none
}

// Server is the fake. Set the options before Start.
type Server struct {
	// Username and Password, when set, are required (AUTH PLAIN or LOGIN).
	Username, Password string
	// Mechanisms advertised for AUTH; default "PLAIN LOGIN".
	Mechanisms string
	// StartTLS offers the STARTTLS upgrade.
	StartTLS bool
	// Implicit speaks TLS from the first byte (port 465 style).
	Implicit bool
	// RejectRcpt refuses every recipient with 550.
	RejectRcpt bool

	Host string
	Port int

	ln   net.Listener
	cert tls.Certificate
	pool *x509.CertPool

	mu       sync.Mutex
	messages []Message
}

// Start listens on a free loopback port and serves until the test ends.
func (s *Server) Start(t testing.TB) *Server {
	t.Helper()
	cert, pool, err := selfSigned()
	if err != nil {
		t.Fatal(err)
	}
	s.cert, s.pool = cert, pool
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln = ln
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	s.Host = host
	s.Port, _ = strconv.Atoi(port)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

// Roots trusts the server's certificate.
func (s *Server) Roots() *x509.CertPool { return s.pool }

// Messages returns what was accepted so far.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.messages...)
}

func (s *Server) tlsConfig() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{s.cert}, MinVersion: tls.VersionTLS12}
}

func (s *Server) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }() // whichever conn is current: plain or TLS
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	secure := false
	if s.Implicit {
		tc := tls.Server(conn, s.tlsConfig())
		if err := tc.Handshake(); err != nil {
			return
		}
		conn, secure = tc, true
	}
	tp := textproto.NewConn(conn)
	say := func(code int, msg string) { _ = tp.PrintfLine("%d %s", code, msg) }
	say(220, "smtptest ready")
	var cur Message
	user := ""
	reset := func() { cur = Message{TLS: secure, Auth: user} }
	reset()
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			ext := []string{"smtptest", "8BITMIME"}
			if s.StartTLS && !secure {
				ext = append(ext, "STARTTLS")
			}
			if s.Username != "" {
				mechs := s.Mechanisms
				if mechs == "" {
					mechs = "PLAIN LOGIN"
				}
				ext = append(ext, "AUTH "+mechs)
			}
			for i, e := range ext {
				sep := "-"
				if i == len(ext)-1 {
					sep = " "
				}
				_ = tp.PrintfLine("250%s%s", sep, e)
			}
		case "STARTTLS":
			if !s.StartTLS || secure {
				say(502, "not available")
				continue
			}
			say(220, "go ahead")
			tc := tls.Server(conn, s.tlsConfig())
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, secure = tc, true
			tp = textproto.NewConn(conn)
			reset()
		case "AUTH":
			mech, initial, _ := strings.Cut(arg, " ")
			var u, p string
			switch strings.ToUpper(mech) {
			case "PLAIN":
				if initial == "" {
					say(334, "")
					initial, _ = tp.ReadLine()
				}
				raw, _ := base64.StdEncoding.DecodeString(initial)
				parts := strings.Split(string(raw), "\x00")
				if len(parts) == 3 {
					u, p = parts[1], parts[2]
				}
			case "LOGIN":
				ask := func(q string) string {
					say(334, base64.StdEncoding.EncodeToString([]byte(q)))
					a, _ := tp.ReadLine()
					raw, _ := base64.StdEncoding.DecodeString(a)
					return string(raw)
				}
				u = ask("Username:")
				p = ask("Password:")
			default:
				say(504, "unknown mechanism")
				continue
			}
			if s.Username == "" || u != s.Username || p != s.Password {
				say(535, "5.7.8 authentication failed")
				continue
			}
			user = u
			reset()
			say(235, "2.7.0 welcome")
		case "MAIL":
			if s.Username != "" && user == "" {
				say(530, "5.7.0 authentication required")
				continue
			}
			reset()
			cur.From = address(arg)
			say(250, "ok")
		case "RCPT":
			if s.RejectRcpt {
				say(550, "5.1.1 no such user")
				continue
			}
			cur.To = append(cur.To, address(arg))
			say(250, "ok")
		case "DATA":
			say(354, "end with .")
			data, err := io.ReadAll(tp.DotReader())
			if err != nil {
				return
			}
			cur.Data = string(data)
			s.mu.Lock()
			s.messages = append(s.messages, cur)
			s.mu.Unlock()
			reset()
			say(250, "queued")
		case "RSET", "NOOP":
			say(250, "ok")
		case "QUIT":
			say(221, "bye")
			return
		default:
			say(502, "unknown command")
		}
	}
}

// address extracts the address of "FROM:<a@b> BODY=8BITMIME".
func address(arg string) string {
	_, rest, _ := strings.Cut(arg, "<")
	addr, _, _ := strings.Cut(rest, ">")
	return addr
}

func selfSigned() (tls.Certificate, *x509.CertPool, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "smtptest"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool, nil
}
