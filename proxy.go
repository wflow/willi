package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"time"

	log "github.com/inconshreveable/log15"

	"github.com/emersion/go-smtp"
)

var ErrRelayAccessDenied = &smtp.SMTPError{
	Code:         554,
	EnhancedCode: smtp.EnhancedCode{5, 7, 1},
	Message:      "Relay access denied",
}

var ErrInternal = &smtp.SMTPError{
	Code:         450,
	EnhancedCode: smtp.NoEnhancedCode,
	Message:      "Internal server error. Please try again later.",
}

type ProxyBackend struct {
	loggers  *SessionLoggers
	domain   string
	mappings []Mapping

	recipientDelimiter string
}

func (b *ProxyBackend) Login(_ *smtp.ConnectionState, username, password string) (smtp.Session, error) {
	return nil, smtp.ErrAuthUnsupported
}

func (b *ProxyBackend) AnonymousLogin(s *smtp.ConnectionState) (smtp.Session, error) {
	logger, ok := b.loggers.Get(s.RemoteAddr)
	if !ok {
		logger = log.New("sid", "") // fallback, should not happen :)
	}

	logger.Debug("TLS", "connection_state", s)
	logger.Debug("HELO/EHLO", "client", s.RemoteAddr, "client_helo", s.Hostname, "tls", s.TLS.HandshakeComplete)

	return &LoggingSession{
		log: logger,
		delegate: &ProxySession{
			log:      logger,
			mappings: b.mappings,

			recipientDelimiter: b.recipientDelimiter,

			clientHelo: s.Hostname,
			clientAddr: s.RemoteAddr,
			clientTls:  s.TLS.HandshakeComplete,

			helo: b.domain,

			msg: buildZeroProxyMessage(),
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
	log      log.Logger
	mappings []Mapping

	recipientDelimiter string

	clientHelo string
	clientAddr net.Addr
	clientTls  bool

	helo string

	msg ProxyMessage // the current message tx
}

type ProxyMessage struct {
	from   string
	rcpts  []string
	server string

	client *smtp.Client // this is the client used to connect to the upstream smtp server!
	tls    bool
	opts   smtp.MailOptions
}

func buildProxyMessage(from string, opts smtp.MailOptions) ProxyMessage {
	return ProxyMessage{
		from:  from,
		rcpts: make([]string, 0),

		opts: opts,
	}
}

func buildZeroProxyMessage() ProxyMessage {
	return buildProxyMessage("", smtp.MailOptions{})
}

func (s *ProxySession) getUpstream(recipient string) (Upstream, error) {
	for _, mapping := range s.mappings {
		server, err := s.lookupRecipient(mapping, recipient)
		if err == ErrNoUpstreamFound {
			continue
		}

		if !strings.Contains(server.Server, ":") {
			server.Server = server.Server + ":25"
		}

		return server, err
	}

	return Upstream{}, ErrNoUpstreamFound
}

func (s *ProxySession) lookupRecipient(mapping Mapping, recipient string) (Upstream, error) {
	// foo+bar@domain.com
	server, err := s.lookupKey(mapping, recipient)
	if err == nil {
		return server, nil
	}

	if err != ErrNoUpstreamFound {
		return Upstream{}, fmt.Errorf("lookup %T: %w", mapping, err)
	}

	if s.recipientDelimiter != "" {
		recipientWithoutSuffix := removeSuffix(recipient, s.recipientDelimiter)

		if recipientWithoutSuffix != recipient {
			// foo@domain.com
			server, err = s.lookupKey(mapping, recipientWithoutSuffix)
			if err == nil {
				return server, nil
			}

			if err != ErrNoUpstreamFound {
				return Upstream{}, fmt.Errorf("lookup %T: %w", mapping, err)
			}
		}
	}

	parts := strings.Split(recipient, "@")
	if len(parts) == 2 {
		domain := parts[1]

		// domain.com
		server, err = s.lookupKey(mapping, domain)
		if err == nil {
			return server, nil
		}

		if err != ErrNoUpstreamFound {
			return Upstream{}, fmt.Errorf("lookup %T: %w", mapping, err)
		}
	}

	return Upstream{}, ErrNoUpstreamFound
}

func (s *ProxySession) lookupKey(mapping Mapping, key string) (Upstream, error) {
	server, err := mapping.Get(key)
	if err == nil {
		s.log.Debug("Lookup match", "mapping", fmt.Sprintf("%T", mapping), "key", key, "result", server)
	}
	if err == ErrNoUpstreamFound {
		s.log.Debug("Lookup miss", "mapping", fmt.Sprintf("%T", mapping), "key", key)
	}

	return server, err
}

func removeSuffix(recipient string, recipientDelimiter string) string {
	parts1 := strings.Split(recipient, recipientDelimiter)
	if len(parts1) == 2 {
		localPart := parts1[0]
		suffixAtDomain := parts1[1]

		parts2 := strings.Split(suffixAtDomain, "@")
		if len(parts2) == 2 {
			domain := parts2[1]

			return fmt.Sprintf("%s@%s", localPart, domain)
		}
	}

	return recipient
}

func xclient(c *textproto.Conn, s *ProxySession) error {
	clientIP := s.clientAddr.(*net.TCPAddr).IP

	var clientHost string
	hostnames, err := net.LookupAddr(clientIP.String())
	if err != nil {
		s.log.Debug("DNS lookup for client failed", "client", s.clientAddr, "error", err)
	}
	if len(hostnames) > 0 {
		clientHost = strings.TrimSuffix(hostnames[0], ".")
	} else {
		clientHost = "[TEMPUNAVAIL]"
	}

	ipStr := clientIP.String()
	if clientIP.To4() == nil {
		ipStr = fmt.Sprintf("IPV6:%s", clientIP)
	}

	// FIXME HELO/NAME must be encoded according to RFC1891 "xtext" (only relevant for non-ascii chars)

	id, err := c.Cmd(fmt.Sprintf("XCLIENT ADDR=%s NAME=%s HELO=%s", ipStr, clientHost, s.clientHelo))
	if err != nil {
		return err
	}

	c.StartResponse(id)
	defer c.EndResponse(id)

	if _, _, err = c.ReadCodeLine(220); err != nil {
		return err
	}

	return nil
}

func (s *ProxySession) Mail(from string, opts smtp.MailOptions) error {
	s.msg = buildProxyMessage(from, opts)
	return nil
}

func (s *ProxySession) Rcpt(to string) error {
	s.msg.rcpts = append(s.msg.rcpts, to)

	if s.msg.client == nil {
		upstream, err := s.getUpstream(to)
		if err == ErrNoUpstreamFound {
			return ErrRelayAccessDenied
		}
		if err != nil {
			return err
		}

		s.msg.server = upstream.Server

		c, err := smtp.Dial(s.msg.server)
		if err != nil {
			return err
		}
		s.msg.client = c

		if err := s.msg.client.Hello(s.helo); err != nil {
			return err
		}

		if ok, _ := s.msg.client.Extension("STARTTLS"); ok && s.clientTls {
			s.log.Debug("Trying STARTTLS with upstream server")

			cfg := &tls.Config{
				InsecureSkipVerify: !upstream.TlsVerify,
			}
			if err := s.msg.client.StartTLS(cfg); err != nil {
				return err
			}
			s.msg.tls = true
		}

		if ok, _ := s.msg.client.Extension("XCLIENT"); ok {
			if err := xclient(s.msg.client.Text, s); err != nil {
				return err
			}
		}

		if err := s.msg.client.Mail(s.msg.from, &s.msg.opts); err != nil {
			return err
		}
	}

	return s.msg.client.Rcpt(to)
}

func (s *ProxySession) Data(r io.Reader) error {
	if s.msg.client == nil {
		return fmt.Errorf("SMTP client is unexpectedly nil")
	}

	w, err := s.msg.client.Data()
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
	if s.msg.client == nil {
		return
	}

	if err := s.msg.client.Quit(); err != nil {
		log.Warn("Error during QUIT with upstream server. Closing connection anyway", "error", err)

		if err = s.msg.client.Close(); err != nil {
			log.Warn("Error while closing connection with upstream server", "error", err)
		}
	}

	s.msg = buildZeroProxyMessage()
}

func (s *ProxySession) Logout() error {
	if s.msg.client == nil {
		return nil
	}

	defer s.msg.client.Close()
	return s.msg.client.Quit()
}

type LoggingSession struct {
	log      log.Logger
	delegate *ProxySession
}

func (s *LoggingSession) Mail(from string, opts smtp.MailOptions) error {
	err := s.delegate.Mail(from, opts)
	s.logDebug(err, "MAIL FROM", "from", from, "opts", opts)

	if err != nil {
		s.log.Info("Message rejected", s.getCanonicalLogLineCtx(err)...)
	}

	return s.wrapAsSMTPError(err)
}

func (s *LoggingSession) Rcpt(to string) error {
	err := s.delegate.Rcpt(to)
	s.logDebug(err, "RCPT TO", "to", to)

	if err != nil {
		s.log.Info("Message rejected", s.getCanonicalLogLineCtx(err)...)
	}

	return s.wrapAsSMTPError(err)
}

func (s *LoggingSession) Data(r io.Reader) error {
	err := s.delegate.Data(r)
	s.logDebug(err, "DATA")

	text := "Message accepted"
	if err != nil {
		text = "Message rejected"
	}

	s.log.Info(text, s.getCanonicalLogLineCtx(err)...)

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

func (s *LoggingSession) getCanonicalLogLineCtx(err error) []interface{} {
	session := s.delegate
	msg := session.msg

	ctx := []interface{}{
		"client", session.clientAddr, "client_helo", session.clientHelo, "client_tls", session.clientTls,
		"from", msg.from, "to", strings.Join(msg.rcpts, ","),
		"upstream", msg.server, "upstream_tls", msg.tls,
	}

	if err != nil {
		ctx = append(ctx, "error", s.formatError(err), "error_src", s.formatErrorSource(err))
	}

	return ctx
}

func (s *LoggingSession) logDebug(err error, msg string, ctx ...interface{}) {
	if err != nil {
		ctx = append(ctx, "error", err)
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

func (s *LoggingSession) formatErrorSource(err error) string {
	switch err.(type) {
	case nil:
		return ""
	case *smtp.SMTPError:
		return "upstream"
	default:
		return "internal"
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
