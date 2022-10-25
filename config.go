package main

import (
	"fmt"
	"os"
	"time"

	units "github.com/docker/go-units"
	"github.com/hjson/hjson-go/v4"
	log "github.com/inconshreveable/log15"
)

type Duration time.Duration
type ByteSize int
type LogLvl log.Lvl

type Config struct {
	LogLevel LogLvl
	Listen   string
	Domain   string

	TlsCert string `json:"tls_cert"`
	TlsKey  string `json:"tls_key"`

	ReadTimeout        Duration `json:"read_timeout"`
	WriteTimeout       Duration `json:"write_timeout"`
	MaxMessageBytes    ByteSize `json:"max_message_bytes"`
	MaxRecipients      int      `json:"max_recipients"`
	RecipientDelimiter string   `json:"recipient_delimiter"`

	Mappings []Mapping `json:"-"`
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

func parseMappings(mappings []interface{}) ([]Mapping, error) {
	list := make([]Mapping, 0)
	for _, m := range mappings {
		v, ok := m.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("mappings: must contain {...} elements but has %T", m)
		}
		mapping, err := parseMapping(v)
		if err != nil {
			return nil, err
		}
		list = append(list, mapping)
	}

	return list, nil
}

func parseMapping(mapping map[string]interface{}) (Mapping, error) {
	t, ok := mapping["type"]
	if !ok {
		return nil, fmt.Errorf("missing 'type:' field")
	}

	mappingType, ok := t.(string)
	if !ok {
		return nil, fmt.Errorf("'type:' must be a string but was %T", t)
	}

	switch mappingType {
	case "static":
		return parseStaticMapping(mapping)
	case "csv":
		return parseCSVMapping(mapping)
	case "sql":
		return parseSQLMapping(mapping)
	default:
		return nil, fmt.Errorf("'type:' must be one of 'static', 'csv', 'sql' but was '%s'", mappingType)
	}
}

func parseStaticMapping(mapping map[string]interface{}) (Mapping, error) {
	s, ok := mapping["server"]
	if !ok {
		return nil, fmt.Errorf("static mapping: missing 'server:'")
	}

	server, ok := s.(string)
	if !ok {
		return nil, fmt.Errorf("static mapping: server must be a string but was %T", s)
	}

	v, ok := mapping["tls_verify"]
	if !ok {
		v = true
	}

	tlsVerify, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("static mapping: tls_verify must be bool, but was %T", v)
	}

	m, err := NewStaticMapping(server, tlsVerify)
	if err != nil {
		return nil, fmt.Errorf("static mapping: %w", err)
	}

	return m, nil
}

func parseCSVMapping(mapping map[string]interface{}) (Mapping, error) {
	f, ok := mapping["file"]
	if !ok {
		return nil, fmt.Errorf("csv mapping: missing 'file:'")
	}

	file, ok := f.(string)
	if !ok {
		return nil, fmt.Errorf("csv mapping: 'file:' must be a string but was %T", f)
	}

	m, err := NewCSVMapping(file)
	if err != nil {
		return nil, fmt.Errorf("csv mapping: %w", err)
	}

	return m, nil
}

func parseSQLMapping(mapping map[string]interface{}) (Mapping, error) {
	c, ok := mapping["connection"]
	if !ok {
		return nil, fmt.Errorf("sql mapping: missing 'connection:'")
	}

	connection, ok := c.(string)
	if !ok {
		return nil, fmt.Errorf("sql mapping: 'connection:' must be a string but was %T", c)
	}

	q, ok := mapping["query"]
	if !ok {
		return nil, fmt.Errorf("sql mapping: missing 'query:'")
	}

	query, ok := q.(string)
	if !ok {
		return nil, fmt.Errorf("sql mapping: 'query:' must be a string but was %T", q)
	}

	m, err := NewSQLMapping("mysql", connection, query)
	if err != nil {
		return nil, fmt.Errorf("sql mapping: %w", err)
	}

	return m, nil
}

func loadConfigFile(configFile string) (*Config, error) {
	d, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	config := Config{
		LogLevel: LogLvl(log.LvlInfo),

		Listen: ":25",
		Domain: getDefaultHostname(),

		ReadTimeout:     Duration(10 * time.Second),
		WriteTimeout:    Duration(10 * time.Second),
		MaxMessageBytes: 20 * units.MiB,
		MaxRecipients:   50,

		Mappings: make([]Mapping, 0),
	}
	if err := hjson.Unmarshal(d, &config); err != nil {
		return nil, err
	}

	var configMap map[string]interface{}
	if err := hjson.Unmarshal(d, &configMap); err != nil {
		return nil, err
	}
	if config.Mappings, err = parseMappings(configMap["mappings"].([]interface{})); err != nil {
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
