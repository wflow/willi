package main

import (
	"crypto/tls"
	"fmt"
	"io"
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
	return &ProxySession{mappings: b.mappings}, nil
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
