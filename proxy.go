package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strings"

	log "github.com/inconshreveable/log15"

	"github.com/emersion/go-smtp"
)

var ErrRelayAccessDenied = &smtp.SMTPError{
	Code:         554,
	EnhancedCode: smtp.EnhancedCode{5, 7, 1},
	Message:      "Relay access denied",
}

// The ProxyBackend implements SMTP server methods.
type ProxyBackend struct {
	domain   string
	mappings []ServerMap
}

func (b *ProxyBackend) Login(_ *smtp.ConnectionState, username, password string) (smtp.Session, error) {
	return nil, smtp.ErrAuthUnsupported
}

func (b *ProxyBackend) AnonymousLogin(s *smtp.ConnectionState) (smtp.Session, error) {
	// FIXME log TLS stuff

	logger := log.New("session", randSeq(10))

	logger.Debug("HELO/EHLO", "client_ip", s.RemoteAddr, "client_helo", s.Hostname)

	return &LoggingSession{
		log: logger,
		delegate: &ProxySession{
			log:      logger,
			mappings: b.mappings,

			clientHelo: s.Hostname,
			clientAddr: s.RemoteAddr,

			helo: b.domain,
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

// A ProxySession is returned after EHLO.
type ProxySession struct {
	log      log.Logger
	mappings []ServerMap

	clientHelo string
	clientAddr net.Addr

	helo string

	msg ProxyMessage // the current message tx
}

// ProxyMessage encapsulates one message transaction (MAIL FROM, RCPT TO*, DATA)
type ProxyMessage struct {
	id string

	from   string
	rcpts  []string
	server string

	client *smtp.Client // this is the client used to connect to the backend smtp server!
	opts   smtp.MailOptions
}

func (s *ProxySession) getServer(recipient string) (string, error) {
	for _, mapping := range s.mappings {
		server, err := mapping.GetServer(recipient)
		if err == nil {
			return server, nil
		}

		if err == ErrNotFound {
			parts := strings.Split(recipient, "@")
			if len(parts) == 2 {
				domain := parts[1]
				return s.getServer(domain)
			}
		}

		if err != nil {
			return "", err
		}
	}

	return "", ErrNotFound
}

func (s *ProxySession) Mail(from string, opts smtp.MailOptions) error {
	s.msg = ProxyMessage{
		id:    randSeq(10),
		from:  from,
		rcpts: make([]string, 0),

		opts: opts,
	}

	return nil
}

func (s *ProxySession) Rcpt(to string) error {
	if s.msg.client == nil {
		s.msg.rcpts = append(s.msg.rcpts, to)

		server, err := s.getServer(to)
		if err == ErrNotFound {
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

		if ok, _ := s.msg.client.Extension("STARTTLS"); ok { // FIXME only allow to skip tls if client connection is also plain?
			s.log.Debug("Trying STARTTLS with backend")

			cfg := &tls.Config{
				//InsecureSkipVerify: true,
			}
			if err := s.msg.client.StartTLS(cfg); err != nil {
				return err // FIXME Retry without TLS instead?
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

	// Message is now queued by backend server

	return nil
}

func (s *ProxySession) Reset() { // called after each message DATA
	if s.msg.client == nil {
		return
	}

	if err := s.msg.client.Quit(); err != nil {
		log.Warn("Error during QUIT with backend. Closing connection anyway", "error", err)

		if err = s.msg.client.Close(); err != nil {
			log.Warn("Error while closing connection with backend", "error", err)
		}
	}

	s.msg = ProxyMessage{}
}

func (s *ProxySession) ResetWithResult() ProxyMessage {
	msg := s.msg
	s.Reset()
	return msg
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
	return s.wrapError(err)
}

func (s *LoggingSession) Rcpt(to string) error {
	err := s.delegate.Rcpt(to)

	s.logDebug(err, "RCPT TO", "to", to)
	return s.wrapError(err)
}

func (s *LoggingSession) Data(r io.Reader) error {
	err := s.delegate.Data(r)

	s.logDebug(err, "DATA")
	return s.wrapError(err)
}

func (s *LoggingSession) Reset() {
	msg := s.delegate.ResetWithResult()

	s.log.Info("Message accepted", "msg", msg.id,
		"client_ip", s.delegate.clientAddr, "client_helo", s.delegate.clientHelo,
		"from", msg.from, "to", strings.Join(msg.rcpts, ","), "relay", msg.server,
		"error", s.lastError)
}

func (s *LoggingSession) Logout() error {
	err := s.delegate.Logout()
	s.logDebug(err, "Logout")
	smtpError := s.wrapError(err)
	return smtpError
}

func (s *LoggingSession) logDebug(err error, msg string, ctx ...interface{}) {
	if err != nil {
		ctx = append(ctx, "error", err)
	}
	s.log.Debug(msg, ctx...)
}

func (s *LoggingSession) wrapError(err error) error {
	s.lastError = err

	switch err.(type) {
	case nil:
		return nil
	case *smtp.SMTPError:
		return err
	default:
		return &smtp.SMTPError{
			Code:         450,
			EnhancedCode: smtp.NoEnhancedCode,
			Message:      "Internal server error",
		}
	}
}
