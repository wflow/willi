package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/textproto"
	"strings"

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
	domain   string
	mappings []Mapping

	recipientDelimiter string
}

func (b *ProxyBackend) Login(_ *smtp.ConnectionState, username, password string) (smtp.Session, error) {
	return nil, smtp.ErrAuthUnsupported
}

func (b *ProxyBackend) AnonymousLogin(s *smtp.ConnectionState) (smtp.Session, error) {
	logger := log.New("sid", randSeq(10))

	logger.Debug("TLS", "connection_state", s)
	logger.Debug("HELO/EHLO", "client_ip", s.RemoteAddr, "client_helo", s.Hostname, "tls", s.TLS.HandshakeComplete)

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

func (s *ProxySession) getServer(recipient string) (string, error) {
	for _, mapping := range s.mappings {
		server, err := s.lookupRecipient(mapping, recipient)
		if err == ErrNoServerFound {
			continue
		}

		return server, err
	}

	return "", ErrNoServerFound
}

func (s *ProxySession) lookupRecipient(mapping Mapping, recipient string) (string, error) {
	if s.recipientDelimiter != "" {
		recipient = removeSuffix(recipient, s.recipientDelimiter)
	}

	server, err := s.lookupKey(mapping, recipient)
	if err == ErrNoServerFound {
		parts := strings.Split(recipient, "@")
		if len(parts) == 2 {
			domain := parts[1]
			server, err = s.lookupKey(mapping, domain)
		}
	}

	if err != nil && err != ErrNoServerFound {
		return "", fmt.Errorf("lookup %T: %w", mapping, err)
	}

	if strings.Contains(server, ":") {
		return server, err
	}

	return fmt.Sprintf("%s:25", server), err
}

func (s *ProxySession) lookupKey(mapping Mapping, key string) (string, error) {
	server, err := mapping.Get(key)
	if err == nil {
		s.log.Debug("Lookup match", "mapping", fmt.Sprintf("%T", mapping), "key", key, "result", server)
	}
	if err == ErrNoServerFound {
		s.log.Debug("Lookup miss", "mapping", fmt.Sprintf("%T", mapping), "key", key)
	}

	return server, err
}

func removeSuffix(recipient string, recipientDelimiter string) string {
	parts1 := strings.Split(recipient, "+")
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

	ipStr := clientIP.String()
	if clientIP.To4() == nil {
		ipStr = fmt.Sprintf("IPV6:%s", clientIP)
	}

	// FIXME HELO name must be encoded s according to RFC1891 "xtext" (only relevant for non-ascii chars)

	id, err := c.Cmd(fmt.Sprintf("XCLIENT ADDR=%s HELO=%s", ipStr, s.clientHelo))
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
		server, err := s.getServer(to)
		if err == ErrNoServerFound {
			return ErrRelayAccessDenied
		}
		if err != nil {
			return err
		}

		s.msg.server = server

		c, err := smtp.Dial(s.msg.server)
		if err != nil {
			return err
		}
		s.msg.client = c

		if err := s.msg.client.Hello(s.helo); err != nil {
			return err
		}

		if ok, _ := s.msg.client.Extension("XCLIENT"); ok {
			if err := xclient(s.msg.client.Text, s); err != nil {
				return err
			}
		}

		if ok, _ := s.msg.client.Extension("STARTTLS"); ok || !s.clientTls { // if client connection is plain, plain is ok
			s.log.Debug("Trying STARTTLS with upstream server")

			cfg := &tls.Config{
				//InsecureSkipVerify: true,
			}
			if err := s.msg.client.StartTLS(cfg); err != nil {
				return err
			}
			s.msg.tls = true
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

	lastError error
}

func (s *LoggingSession) Mail(from string, opts smtp.MailOptions) error {
	err := s.delegate.Mail(from, opts)
	s.logDebug(err, "MAIL FROM", "from", from, "opts", opts)

	if err != nil {
		s.log.Info("Message rejected", s.getCanonicalLogLineCtx()...)
	}

	return s.wrapAsSMTPError(err)
}

func (s *LoggingSession) Rcpt(to string) error {
	err := s.delegate.Rcpt(to)
	s.logDebug(err, "RCPT TO", "to", to)

	if err != nil {
		s.log.Info("Message rejected", s.getCanonicalLogLineCtx()...)
	}

	return s.wrapAsSMTPError(err)
}

func (s *LoggingSession) Data(r io.Reader) error {
	err := s.delegate.Data(r)
	s.logDebug(err, "DATA")

	if err == nil {
		s.log.Info("Message accepted", s.getCanonicalLogLineCtx()...)
	}

	return s.wrapAsSMTPError(err)
}

func (s *LoggingSession) Reset() {
	// Called after each DATA, but also if client sends RSET

	s.delegate.Reset()
	s.log.Debug("Reset")

	s.lastError = nil
}

func (s *LoggingSession) Logout() error {
	// Called when client disconnects (QUIT), or closes the connection

	err := s.delegate.Logout()
	s.logDebug(err, "Logout")

	if s.lastError != nil {
		s.log.Info("Message rejected", s.getCanonicalLogLineCtx()...)
	}

	smtpError := s.wrapAsSMTPError(err)
	s.lastError = nil

	return smtpError
}

func (s *LoggingSession) getCanonicalLogLineCtx() []interface{} {
	session := s.delegate
	msg := session.msg

	ctx := []interface{}{
		"client_ip", s.formatIP(session.clientAddr), "client_helo", session.clientHelo, "client_tls", session.clientTls,
		"from", msg.from, "to", strings.Join(msg.rcpts, ","),
		"upstream", msg.server, "upstream_tls", msg.tls,
	}

	if s.lastError != nil {
		ctx = append(ctx, "error", s.formatError(s.lastError), "error_src", s.formatErrorSource(s.lastError))
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
	s.lastError = err

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

func (s *LoggingSession) formatIP(addr net.Addr) string {
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		return tcpAddr.IP.String()
	}

	return addr.String() // safety fallback, should never happen
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
