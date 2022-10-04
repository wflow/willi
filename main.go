package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"

	"github.com/emersion/go-smtp"
	log "github.com/inconshreveable/log15"
)

var (
	configFileFlag = flag.String("c", "config.conf", "Path to configuration file")
	versionFlag    = flag.Bool("V", false, "Print version and exit")
	verboseFlag    = flag.Bool("v", false, "Verbose output")
	version        = "undefined" // updated during release build
)

func main() {

	flag.Parse()

	if *versionFlag {
		fmt.Printf("UNNAMED - version %s\n", version)
		os.Exit(0)
	}

	logLevel := log.LvlInfo
	if *verboseFlag {
		logLevel = log.LvlDebug
	}
	log.Root().SetHandler(log.LvlFilterHandler(logLevel, log.StdoutHandler))

	log.Info("Loading config file", "config", *configFileFlag)
	config, err := loadConfigFile(*configFileFlag)
	if err != nil {
		log.Error("Failed to load config file", "error", err)
		os.Exit(1)
	}

	var tlsConfig *tls.Config
	if config.tlsCert != "" && config.tlsKey != "" {

		cer, err := tls.LoadX509KeyPair(config.tlsCert, config.tlsKey)
		if err != nil {
			log.Error("Failed to load TLS key/cert", "error", err)
			os.Exit(1)
		}

		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cer}}
	}

	mappings := make([]ServerMap, 0)
	for _, mappingConfig := range config.mappingConfigs {
		mapping, err := mappingConfig.CreateMapping()
		if err != nil {
			log.Error("Failed to create mapping", "error", err)
			os.Exit(1)
		}
		mappings = append(mappings, mapping)
	}

	be := &ProxyBackend{
		mappings: mappings,
	}

	s := smtp.NewServer(be)

	s.Addr = config.listen
	s.Domain = config.domain
	s.ReadTimeout = config.readTimeout
	s.WriteTimeout = config.writeTimeout
	s.MaxMessageBytes = config.maxMessageBytes
	s.MaxRecipients = config.maxRecipients
	s.AuthDisabled = true
	s.TLSConfig = tlsConfig

	log.Info("Starting server", "address", s.Addr)
	if err := s.ListenAndServe(); err != nil {
		log.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}
