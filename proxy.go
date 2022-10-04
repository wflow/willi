package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
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

func (b *ProxyBackend) AnonymousLogin(_ *smtp.ConnectionState) (smtp.Session, error) {
	// FIXME log TLS stuff
	return &LoggingSession{
		delegate: &ProxySession{mappings: b.mappings},
		log:      log.New("session", randSeq(10)),
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
	from     string
	opts     smtp.MailOptions
	client   *smtp.Client
	mappings []ServerMap
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
	s.opts = opts
	s.client = nil
	return nil
}

func (s *ProxySession) Rcpt(to string) error {
	if s.client == nil {
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

	return nil
}

func (s *ProxySession) Reset() {
	if s.client == nil {
		return
	}

	err := s.client.Reset() // TODO close client, may use new backend now?
	if err != nil {
		// FIXME log.Println(err)
	}
}

func (s *ProxySession) Logout() error {
	if s.client == nil {
		return nil
	}
	defer s.client.Close()

	return s.client.Quit()
}

type LoggingSession struct {
	delegate smtp.Session
	log      log.Logger
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
	var err error
	defer func() { s.logDebug(err, "DATA") }()
	err = s.delegate.Data(r)
	return s.wrapError(err)
}

func (s *LoggingSession) Reset() {
	s.log.Debug("Reset")
	s.delegate.Reset()
}

func (s *LoggingSession) Logout() error {
	// TODO log canonical log line

	// from= to= size= relay= status= (tls=)

	var err error
	defer func() { s.logDebug(err, "Logout") }()
	err = s.delegate.Logout()
	return s.wrapError(err)
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
