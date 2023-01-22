package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	log "github.com/inconshreveable/log15"
)

var (
	configFileFlag = flag.String("c", "lilli.conf", "Path to configuration file")
	versionFlag    = flag.Bool("V", false, "Print version and exit")
	version        = "undefined" // updated during release build
)

func main() {
	rand.Seed(time.Now().UnixNano())

	flag.Parse()

	if *versionFlag {
		fmt.Printf("lilli smtp proxy - version %s\n", version)
		os.Exit(0)
	}

	fmt.Fprintf(os.Stderr, "Loading config file %s\n", *configFileFlag)
	config, err := loadConfigFile(*configFileFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config file %s: %v\n", *configFileFlag, err)
		os.Exit(1)
	}

	log.Root().SetHandler(
		log.LvlFilterHandler(log.Lvl(config.LogLevel),
			log.StreamHandler(os.Stdout, LogfmtFormatWithoutTimestamp())))

	log.Info("Starting lilli", "version", version)

	var tlsConfig *tls.Config
	if config.TlsCert != "" && config.TlsKey != "" {
		cer, err := tls.LoadX509KeyPair(config.TlsCert, config.TlsKey)
		if err != nil {
			log.Error("Failed to load TLS key/cert", "error", err)
			os.Exit(1)
		}

		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cer},
			MinVersion:   tls.VersionTLS10,
			CipherSuites: []uint16{
				tls.TLS_RSA_WITH_RC4_128_SHA,
				tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
				tls.TLS_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_RSA_WITH_AES_256_CBC_SHA,
				tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
				tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_RC4_128_SHA,
				tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			},
		}
	}

	loggers := &SessionLoggers{
		loggers: make(map[net.Addr]log.Logger),
	}

	be := &ProxyBackend{
		loggers: loggers,
		config:  config,
	}

	s := smtp.NewServer(be)

	s.Addr = config.Listen
	s.Domain = config.Domain
	s.ReadTimeout = time.Duration(config.ReadTimeout)
	s.WriteTimeout = time.Duration(config.WriteTimeout)
	s.MaxMessageBytes = int(config.MaxMessageBytes)
	s.MaxRecipients = config.MaxRecipients
	s.AllowInsecureAuth = true
	s.TLSConfig = tlsConfig

	s.EnableAuth(sasl.Login, func(conn *smtp.Conn) sasl.Server {
		return sasl.NewLoginServer(func(username, password string) error {
			return conn.Session().AuthPlain(username, password)
		})
	})

	log.Info("Starting server", "address", s.Addr)
	log.Info("Config", "tls", config.Tls, "upstream", config.Upstream, "upstream_tls", config.UpstreamTls)

	switch config.Tls {
	case TlsModeNone, TlsModeStartTls:
		err = ListenAndServe(s, loggers)
	case TlsModeSmtps:
		err = ListenAndServeTLS(s, loggers)
	}

	if err != nil {
		log.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}

func ListenAndServe(s *smtp.Server, loggers *SessionLoggers) error {
	network := "tcp"
	if s.LMTP {
		network = "unix"
	}

	addr := s.Addr
	if !s.LMTP && addr == "" {
		addr = ":smtp"
	}

	l, err := net.Listen(network, addr)
	if err != nil {
		return err
	}

	return s.Serve(&SessionListener{l: l, loggers: loggers})
}

func ListenAndServeTLS(s *smtp.Server, loggers *SessionLoggers) error {
	if s.LMTP {
		return fmt.Errorf("Cannot use LMTP and TLS")
	}

	addr := s.Addr
	if addr == "" {
		addr = ":smtps"
	}

	l, err := tls.Listen("tcp", addr, s.TLSConfig)
	if err != nil {
		return err
	}

	return s.Serve(&SessionListener{l: l, loggers: loggers})
}
