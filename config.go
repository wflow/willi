package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	units "github.com/docker/go-units"
	log "github.com/inconshreveable/log15"
	toml "github.com/pelletier/go-toml"
)

type Config struct {
	listen string
	domain string

	tlsCert string
	tlsKey  string

	readTimeout     time.Duration
	writeTimeout    time.Duration
	maxMessageBytes int
	maxRecipients   int

	mappingConfigs []MappingConfig
}

type MappingConfig interface {
	CreateMapping() (ServerMap, error)
}

type MySQLMappingConfig struct {
	connection string
	query      string
}

func (c *MySQLMappingConfig) CreateMapping() (ServerMap, error) {
	return NewMySQLServerMap(c.connection, c.query)
}

type CSVMappingConfig struct {
	file string
}

func (c *CSVMappingConfig) CreateMapping() (ServerMap, error) {
	return NewCSVServerMap(c.file)
}

type StaticMappingConfig struct {
	server string
	port   int
}

func (c *StaticMappingConfig) CreateMapping() (ServerMap, error) {
	return NewStaticServerMap(c.server, c.port)
}

func loadConfigFile(configFile string) (Config, error) {
	if _, err := os.Stat(configFile); err != nil {
		return Config{}, err
	}

	tomlConfig, err := toml.LoadFile(configFile)
	if err != nil {
		return Config{}, err
	}

	config := Config{
		listen: getConfigValue(tomlConfig, "server.listen"),
		domain: getConfigValueDefault(tomlConfig, "server.domain", getDefaultHostname()),

		tlsCert: getConfigValueDefault(tomlConfig, "server.tls_cert", ""),
		tlsKey:  getConfigValueDefault(tomlConfig, "server.tls_key", ""),

		readTimeout:     parseDuration(getConfigValueDefault(tomlConfig, "server.read_timeout", "10s")),
		writeTimeout:    parseDuration(getConfigValueDefault(tomlConfig, "server.write_timeout", "10s")),
		maxMessageBytes: parseSize(getConfigValueDefault(tomlConfig, "server.max_message_bytes", "20mb")),
		maxRecipients:   parseInt(getConfigValueDefault(tomlConfig, "server.max_recipients", "50")),

		mappingConfigs: make([]MappingConfig, 0),
	}

	x := tomlConfig.Get("mappings")
	mappings, ok := x.(*toml.Tree)
	if !ok {
		return Config{}, fmt.Errorf("Config file must define at least one [mappings.XXX] section")
	}

	for _, key := range mappings.Keys() {
		mapping, ok := mappings.Get(key).(*toml.Tree)
		if !ok {
			return Config{}, fmt.Errorf("Not a mapping section: %s", key)
		}

		t, ok := mapping.Get("type").(string)
		if !ok {
			return Config{}, fmt.Errorf("Section [mappings.%s] must contain type=", key)
		}
		switch t {
		case "static":
			log.Info("Loading static mapping", "key", key)
			config.mappingConfigs = append(config.mappingConfigs, &StaticMappingConfig{
				server: mapping.Get("server").(string),
				port:   int(mapping.Get("port").(int64)),
			})
		case "sql":
			log.Info("Loading SQL mapping", "key", key)
			config.mappingConfigs = append(config.mappingConfigs, &MySQLMappingConfig{
				connection: mapping.Get("connection").(string),
				query:      mapping.Get("query").(string),
			})
		case "csv":
			log.Info("Loading CSV mapping", "key", key)
			config.mappingConfigs = append(config.mappingConfigs, &CSVMappingConfig{
				file: mapping.Get("file").(string),
			})
		default:
			return Config{}, fmt.Errorf("Config file: Unknown mapping type: %s", t)
		}
	}

	return config, nil
}

func getDefaultHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Warn("Failed to get default hostname", "error", err)
		hostname = "localhost"
	}

	return hostname
}

func getConfigValueDefault(config *toml.Tree, key string, defaultVal string) string {
	val := config.Get(key)
	if val == nil {
		return defaultVal
	}

	return val.(string)
}

func getConfigValue(config *toml.Tree, key string) string {
	val := config.Get(key)
	if val == nil {
		log.Error("Invalid configuration file: Mandatory key is missing", "key", key)
		os.Exit(1)
	}

	return val.(string)
}

func getBoolConfigValueDefault(config *toml.Tree, key string, defaultVal bool) bool {
	defaultStringValue := "no"
	if defaultVal {
		defaultStringValue = "yes"
	}

	return getConfigValueDefault(config, key, defaultStringValue) == "yes"
}

func parseDuration(valStr string) time.Duration {
	val, err := time.ParseDuration(valStr)
	if err != nil {
		log.Error("Invalid configuration file: Invalid duration", "value", valStr, "error", err)
		os.Exit(1)
	}
	return val
}

func parseSize(valStr string) int {
	val, err := units.FromHumanSize(valStr)
	if err != nil {
		log.Error("Invalid configuration file: Invalid size", "value", valStr, "error", err)
		os.Exit(1)
	}
	return int(val) // safe enough. If you want to process mails with > 2GB, you're in trouble anyway
}

func parseInt(valStr string) int {
	val, err := strconv.Atoi(valStr)
	if err != nil {
		log.Error("Invalid configuration file: Not a numerical value", "value", valStr, "error", err)
		os.Exit(1)
	}
	return val
}
