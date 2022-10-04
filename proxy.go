package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/emersion/go-smtp"
)

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
		log.Println("Checking mapping", mapping)
		server, err := mapping.GetServer(recipient)
		if err == nil {
			log.Println("Found", server, "for", recipient)
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

	return "", fmt.Errorf("No mapping found for %s", recipient)
}

func (s *ProxySession) Mail(from string, opts smtp.MailOptions) error {
	s.from = from
	s.opts = opts
	s.client = nil

	log.Println("Mail from:", from)
	return nil
}

func (s *ProxySession) Rcpt(to string) error {
	if s.client == nil {
		server, err := s.getServer(to)
		if err != nil {
			return err // TODO convert to something like 'relay access denied'
		}
		log.Println("Using server: " + server)

		c, err := smtp.Dial(server)
		if err != nil {
			return err
		}
		s.client = c

		hostname := ""
		hostname, err = os.Hostname()
		if err != nil {
			log.Println(err)
			hostname = "localhost"
		}

		log.Println("Replay: HELO " + hostname)
		if err := s.client.Hello(hostname); err != nil {
			log.Println(err)
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
		log.Println("SMTP client is unexpectedly nil")
		return &smtp.SMTPError{Code: 500, EnhancedCode: smtp.EnhancedCode{5, 0, 0}, Message: "Invalid SMTP command order"}
	}

	w, err := s.client.Data()
	if err != nil {
		log.Println(err)
		return err
	}

	if _, err := io.Copy(w, r); err != nil {
		return err
	}

	if err := w.Close(); err != nil {
		return err
	}

	log.Println("-----------")

	return nil
}

func (s *ProxySession) Reset() {
	log.Println("Reset")
	if s.client == nil {
		return
	}

	err := s.client.Reset() // TODO close client, may use new backend now?
	if err != nil {
		log.Println(err)
	}
}

func (s *ProxySession) Logout() error {
	log.Println("Logout")
	if s.client == nil {
		return nil
	}
	defer s.client.Close()

	return s.client.Quit()
}
