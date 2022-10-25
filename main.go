package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/emersion/go-smtp"
	log "github.com/inconshreveable/log15"
)

var (
	configFileFlag = flag.String("c", "willi.conf", "Path to configuration file")
	versionFlag    = flag.Bool("V", false, "Print version and exit")
	version        = "undefined" // updated during release build
)

func main() {
	rand.Seed(time.Now().UnixNano())

	flag.Parse()

	if *versionFlag {
		fmt.Printf("willi smtp proxy - version %s\n", version)
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

	log.Info("Starting willi", "version", version)

	for _, mapping := range config.Mappings {
		log.Info("Using mapping", "mapping", mapping)
	}

	var tlsConfig *tls.Config
	if config.TlsCert != "" && config.TlsKey != "" {
		cer, err := tls.LoadX509KeyPair(config.TlsCert, config.TlsKey)
		if err != nil {
			log.Error("Failed to load TLS key/cert", "error", err)
			os.Exit(1)
		}

		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cer}}
	}

	loggers := &SessionLoggers{
		loggers: make(map[net.Addr]log.Logger),
	}

	be := &ProxyBackend{
		loggers:  loggers,
		domain:   config.Domain,
		mappings: config.Mappings,

		recipientDelimiter: config.RecipientDelimiter,
	}

	s := smtp.NewServer(be)

	s.Addr = config.Listen
	s.Domain = config.Domain
	s.ReadTimeout = time.Duration(config.ReadTimeout)
	s.WriteTimeout = time.Duration(config.WriteTimeout)
	s.MaxMessageBytes = int(config.MaxMessageBytes)
	s.MaxRecipients = config.MaxRecipients
	s.AuthDisabled = true
	s.TLSConfig = tlsConfig

	log.Info("Starting server", "address", s.Addr)
	if err := ListenAndServe(s, loggers); err != nil {
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
