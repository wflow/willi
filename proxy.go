package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"time"

	log "github.com/inconshreveable/log15"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

var ErrInternal = &smtp.SMTPError{
	Code:         450,
	EnhancedCode: smtp.NoEnhancedCode,
	Message:      "Internal server error. Please try again later.",
}

type ProxyBackend struct {
	loggers *SessionLoggers
	config  *Config
}

func (b *ProxyBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	logger, ok := b.loggers.Get(c.Conn().RemoteAddr())
	if !ok {
		logger = log.New("sid", "") // fallback, should not happen :)
	}
	s, _ := c.TLSConnectionState()
	logger.Debug("TLS", "connection_state", s)
	logger.Debug("HELO/EHLO", "client", c.Conn().RemoteAddr(), "client_helo", c.Hostname(), "tls", s.HandshakeComplete)

	upstream := b.config.Upstream

	var client *smtp.Client
	var err error

	tlsCfg := &tls.Config{
		InsecureSkipVerify: !b.config.UpstreamTlsVerify,
	}

	switch b.config.UpstreamTls {
	case TlsModeNone, TlsModeStartTls:
		client, err = smtp.Dial(upstream)
	case TlsModeSmtps:
		client, err = smtp.DialTLS(upstream, tlsCfg)
	}
	if err != nil {
		return nil, err
	}

	if err := client.Hello(c.Hostname()); err != nil {
		return nil, err
	}

	if b.config.UpstreamTls == TlsModeStartTls {
		if err := client.StartTLS(tlsCfg); err != nil {
			return nil, err
		}
	}

	return &LoggingSession{
		log: logger,
		delegate: &ProxySession{
			log:    logger,
			client: client,
		},
	}, nil
}

// https://stackoverflow.com/a/22892986 - because I'm lazy
var letters = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func randSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

type ProxySession struct {
	log    log.Logger
	client *smtp.Client
}

func (s *ProxySession) AuthPlain(username, password string) error {
	return s.client.Auth(sasl.NewPlainClient("", username, password))
}

func (s *ProxySession) Mail(from string, opts *smtp.MailOptions) error {
	return s.client.Mail(from, opts)
}

func (s *ProxySession) Rcpt(to string) error {
	return s.client.Rcpt(to)
}

func (s *ProxySession) Data(r io.Reader) error {
	w, err := s.client.Data()
	if err != nil {
		return err
	}

	if _, err := io.Copy(w, r); err != nil {
		return err
	}

	if err := w.Close(); err != nil {
		return err
	}

	// Message is now queued by upstream server

	return nil
}

func (s *ProxySession) Reset() { // called after each message DATA
	s.client.Reset()
}

func (s *ProxySession) Logout() error {
	err := s.client.Quit()
	if err != nil {
		err = s.client.Close()
	}

	return err
}

type LoggingSession struct {
	log      log.Logger
	delegate *ProxySession
}

func (s *LoggingSession) AuthPlain(username, password string) error {
	err := s.delegate.AuthPlain(username, password)
	s.logDebug(err, "AUTH PLAIN", "user", username)

	return s.wrapAsSMTPError(err)
}

func (s *LoggingSession) Mail(from string, opts *smtp.MailOptions) error {
	err := s.delegate.Mail(from, opts)
	s.logDebug(err, "MAIL FROM", "from", from, "opts", opts)
	return s.wrapAsSMTPError(err)
}

func (s *LoggingSession) Rcpt(to string) error {
	err := s.delegate.Rcpt(to)
	s.logDebug(err, "RCPT TO", "to", to)
	return s.wrapAsSMTPError(err)
}

func (s *LoggingSession) Data(r io.Reader) error {
	err := s.delegate.Data(r)
	s.logDebug(err, "DATA")
	return s.wrapAsSMTPError(err)
}

func (s *LoggingSession) Reset() {
	// Called after each DATA, but also if client sends RSET

	s.delegate.Reset()
	s.log.Debug("Reset")
}

func (s *LoggingSession) Logout() error {
	// Called when client disconnects (QUIT), or closes the connection

	err := s.delegate.Logout()
	s.logDebug(err, "Logout")

	return s.wrapAsSMTPError(err)
}

func (s *LoggingSession) logDebug(err error, msg string, ctx ...interface{}) {
	if err != nil {
		ctx = append(ctx, "error", s.formatError(err))
	}
	s.log.Debug(msg, ctx...)
}

func (s *LoggingSession) wrapAsSMTPError(err error) error {
	switch err.(type) {
	case nil:
		return nil
	case *smtp.SMTPError:
		return err
	default:
		return ErrInternal
	}
}

func (s *LoggingSession) formatError(err error) string {
	switch err.(type) {
	case nil:
		return "nil"
	case *smtp.SMTPError:
		smtpErr := err.(*smtp.SMTPError)

		return fmt.Sprintf("%d %d.%d.%d %s", smtpErr.Code,
			smtpErr.EnhancedCode[0], smtpErr.EnhancedCode[1], smtpErr.EnhancedCode[2],
			smtpErr.Message)
	default:
		return err.Error()
	}
}

type SessionLoggers struct {
	loggers map[net.Addr]log.Logger
	lock    sync.RWMutex
}

func (s *SessionLoggers) New(addr net.Addr) log.Logger {
	s.lock.Lock()
	defer s.lock.Unlock()

	l := log.New("sid", randSeq(10))
	s.loggers[addr] = l
	return l
}

func (s *SessionLoggers) Delete(addr net.Addr) (log.Logger, bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	l, ok := s.loggers[addr]
	delete(s.loggers, addr)
	return l, ok
}

func (s *SessionLoggers) Get(addr net.Addr) (log.Logger, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	l, ok := s.loggers[addr]
	return l, ok
}

type SessionListener struct {
	l       net.Listener
	loggers *SessionLoggers
}

func (l *SessionListener) Accept() (net.Conn, error) {
	c, err := l.l.Accept()

	logger := l.loggers.New(c.RemoteAddr())
	if err == nil {
		logger.Debug("Client connected", "client", c.RemoteAddr())
	} else {
		logger.Debug("Client connect failed", "client", c.RemoteAddr(), "error", err)
	}

	return &SessionConn{c: c, loggers: l.loggers}, err
}

func (l *SessionListener) Addr() net.Addr {
	return l.l.Addr()
}

func (l *SessionListener) Close() error {
	return l.l.Close()
}

type SessionConn struct {
	c       net.Conn
	loggers *SessionLoggers
}

func (c *SessionConn) Read(b []byte) (n int, err error) {
	return c.c.Read(b)
}

func (c *SessionConn) Write(b []byte) (n int, err error) {
	return c.c.Write(b)
}

func (c *SessionConn) Close() error {
	err := c.c.Close()
	l, ok := c.loggers.Delete(c.RemoteAddr())

	if ok {
		if err == nil {
			l.Debug("Client disconnected")
		} else {
			l.Debug("Client disconnect failed", "error", err)
		}
	}

	return err
}

func (c *SessionConn) LocalAddr() net.Addr {
	return c.c.LocalAddr()
}

func (c *SessionConn) RemoteAddr() net.Addr {
	return c.c.RemoteAddr()
}

func (c *SessionConn) SetDeadline(t time.Time) error {
	return c.c.SetDeadline(t)
}

func (c *SessionConn) SetReadDeadline(t time.Time) error {
	return c.c.SetReadDeadline(t)
}

func (c *SessionConn) SetWriteDeadline(t time.Time) error {
	return c.c.SetWriteDeadline(t)
}
