package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	units "github.com/docker/go-units"
	"github.com/hjson/hjson-go/v4"
	log "github.com/inconshreveable/log15"
)

type Duration time.Duration
type ByteSize int
type LogLvl log.Lvl
type TlsMode string

const (
	TlsModeNone     TlsMode = "none"
	TlsModeSmtps    TlsMode = "smtps"
	TlsModeStartTls TlsMode = "starttls"
)

type Config struct {
	LogLevel LogLvl

	Listen string
	Tls    TlsMode `json:"tls"`

	TlsCert string `json:"tls_cert"`
	TlsKey  string `json:"tls_key"`

	Domain          string
	ReadTimeout     Duration `json:"read_timeout"`
	WriteTimeout    Duration `json:"write_timeout"`
	MaxMessageBytes ByteSize `json:"max_message_bytes"`
	MaxRecipients   int      `json:"max_recipients"`

	Upstream          string
	UpstreamTls       TlsMode `json:"upstream_tls"`
	UpstreamTlsVerify bool    `json:"upstream_tls_verify"`
}

func (l *LogLvl) UnmarshalText(b []byte) error {
	x, err := log.LvlFromString(string(b))
	if err != nil {
		return err
	}
	*l = LogLvl(x)
	return nil
}

func (d *Duration) UnmarshalText(b []byte) error {
	x, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(x)
	return nil
}

func (s *ByteSize) UnmarshalText(b []byte) error {
	x, err := units.FromHumanSize(string(b))
	if err != nil {
		return err
	}
	*s = ByteSize(x)
	return nil
}

func (m *TlsMode) UnmarshalText(b []byte) error {
	s := strings.ToLower(string(b))
	switch s {
	case "none", "smtps", "starttls":
		*m = TlsMode(s)
	default:
		return fmt.Errorf("TLS mode must be one of 'none', 'smtps', 'starttls' but was '%s'", s)
	}

	return nil
}

func loadConfigFile(configFile string) (*Config, error) {
	d, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	config := Config{
		LogLevel: LogLvl(log.LvlInfo),

		Listen: ":25",
		Tls:    TlsModeNone,

		UpstreamTls:       TlsModeNone,
		UpstreamTlsVerify: true,

		Domain:          getDefaultHostname(),
		ReadTimeout:     Duration(10 * time.Second),
		WriteTimeout:    Duration(10 * time.Second),
		MaxMessageBytes: 20 * units.MiB,
		MaxRecipients:   50,
	}

	if err := hjson.Unmarshal(d, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func getDefaultHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}

	return hostname
}
