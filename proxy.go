package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
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

	from   string
	rcpt   []string
	opts   smtp.MailOptions
	client *smtp.Client // this is the client used to connect to the backend smtp server!
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
	s.from = from
	s.rcpt = make([]string, 0)
	s.opts = opts
	s.client = nil
	return nil
}

func (s *ProxySession) Rcpt(to string) error {
	if s.client == nil {
		s.rcpt = append(s.rcpt, to)

		server, err := s.getServer(to)
		if err == ErrNotFound {
			return ErrRelayAccessDenied
		}
		if err != nil {
			return err
		}

		c, err := smtp.Dial(server)
		if err != nil {
			return err
		}
		s.client = c

		hostname := ""
		hostname, err = os.Hostname()
		if err != nil {
			log.Warn("Failed to get hostname. Using localhost", "error", err)
			hostname = "localhost"
		}

		if err := s.client.Hello(hostname); err != nil {
			return err
		}

		if ok, _ := s.client.Extension("STARTTLS"); ok {
			cfg := &tls.Config{
				//InsecureSkipVerify: true,
			}
			if err := s.client.StartTLS(cfg); err != nil {
				return err // FIXME Retry without TLS instead?
			}
		}

		if err := s.client.Mail(s.from, &s.opts); err != nil {
			return err
		}
	}

	return s.client.Rcpt(to)
}

func (s *ProxySession) Data(r io.Reader) error {
	if s.client == nil {
		return fmt.Errorf("SMTP client is unexpectedly nil")
	}

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

	// Message is now queued by backend server

	return nil
}

func (s *ProxySession) Reset() { // called after each message DATA
	if s.client == nil {
		return
	}

	// Log here?

	// Restart from scratch (client = nil, require mail from, rcpt to, ...)
	// s.message = ProxyMessage{}
	s.client = nil
	s.from = ""
	s.rcpt = make([]string, 0)

	if err := s.client.Quit(); err != nil {
		log.Warn("Error during QUIT with backend. Closing connection anyway", "error", err)

		if err = s.client.Close(); err != nil {
			log.Warn("Error while closing connection with backend", "error", err)
		}
	}
}

func (s *ProxySession) Logout() error {
	if s.client == nil {
		return nil
	}

	s.log.Info("Logout", "client_ip", s.clientAddr, "client_helo", s.clientHelo, "from", s.from, "to", s.rcpt)

	defer s.client.Close()
	return s.client.Quit()
}

type LoggingSession struct {
	log      log.Logger
	delegate *ProxySession
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
	s.log.Debug("Reset")
	s.delegate.Reset()
}

func (s *LoggingSession) Logout() error {
	err := s.delegate.Logout()

	s.logDebug(err, "Logout")
	smtpError := s.wrapError(err)

	// TODO log canonical log line
	// from= to= size= relay= status= (tls=)

	return smtpError
}

func (s *LoggingSession) logDebug(err error, msg string, ctx ...interface{}) {
	if err != nil {
		ctx = append(ctx, "error", err)
	}
	s.log.Debug(msg, ctx...)
}

func (s *LoggingSession) wrapError(err error) error {
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
