package main

import (
	"crypto/tls"
	"flag"
	"log"
	"os"

	"github.com/emersion/go-smtp"
)

var (
	versionFlag    = flag.Bool("V", false, "Print version and exit")
	configFileFlag = flag.String("c", "config.conf", "Path to configuration file")
	version        = "undefined" // updated during release build
)

const (
	defaultDebug    = false
	defaultListen   = "127.0.0.1:10025"
	defaultCertsDir = "."
)

func main() {
	// Remove date + time from logging output (systemd adds those for us)
	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))

	flag.Parse()

	if *versionFlag {
		log.Printf("UNNAMED - version %s\n", version)
		os.Exit(0)
	}

	log.Println("Loading config file", *configFileFlag)
	config := loadConfigFile(*configFileFlag)

	var tlsConfig *tls.Config
	if config.tlsCert != "" && config.tlsKey != "" {

		cer, err := tls.LoadX509KeyPair(config.tlsCert, config.tlsKey)
		if err != nil {
			log.Fatal(err)
		}

		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cer}}
	}

	mappings := make([]ServerMap, 0)
	for _, mappingConfig := range config.mappingConfigs {
		mapping, err := mappingConfig.CreateMapping()
		if err != nil {
			log.Fatal(err)
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

	log.Println("Starting server at", s.Addr)
	if err := s.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
