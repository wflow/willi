package main

import (
	"log"
	"os"
	"strconv"
	"time"

	units "github.com/docker/go-units"
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
	tls    string
}

func (c *StaticMappingConfig) CreateMapping() (ServerMap, error) {
	return NewStaticServerMap(c.server, c.port, c.tls)
}

func loadConfigFile(configFile string) Config {
	if _, err := os.Stat(configFile); err != nil {
		log.Fatalf("Failed to open config file: %s", err)
	}

	tomlConfig, err := toml.LoadFile(configFile)
	if err != nil {
		log.Fatalf("Config file %s is malformed: %s", configFile, err)
	}

	config := Config{
		listen: getConfigValueDefault(tomlConfig, "server.listen", "127.0.0.1:1025"),
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
		log.Fatalf("Config file must define at least one [mappings.XXX] section")
	}

	for _, key := range mappings.Keys() {
		mapping, ok := mappings.Get(key).(*toml.Tree)
		if !ok {
			log.Fatalf("Not a mapping section: %s", key)
		}

		t, ok := mapping.Get("type").(string)
		if !ok {
			log.Fatalf("Section [mappings.%s] must contain a type= key", key)
		}
		switch t {
		case "static":
			log.Println("Loading static mapping", key)
			config.mappingConfigs = append(config.mappingConfigs, &StaticMappingConfig{
				server: mapping.Get("server").(string),
				port:   int(mapping.Get("port").(int64)),
				tls:    mapping.Get("tls").(string),
			})
		case "sql":
			log.Println("Loading SQL mapping", key)
			config.mappingConfigs = append(config.mappingConfigs, &MySQLMappingConfig{
				connection: mapping.Get("connection").(string),
				query:      mapping.Get("query").(string),
			})
		case "csv":
			log.Println("Loading CSV mapping", key)
			config.mappingConfigs = append(config.mappingConfigs, &CSVMappingConfig{
				file: mapping.Get("file").(string),
			})
		default:
			log.Fatal("Config file: Unknown mapping type", t)
		}
	}

	return config
}

func getDefaultHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Println(err)
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
		log.Fatalf("Invalid configuration file: Mandatory setting '%s' is missing", key)
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
		log.Fatalf("Invalid configuration file: '%s' is not a valid duration: %v", valStr, err)
	}
	return val
}

func parseSize(valStr string) int {
	val, err := units.FromHumanSize(valStr)
	if err != nil {
		log.Fatalf("Invalid configuration file: '%s' is not a valid size: %v", valStr, err)
	}
	return int(val) // safe enough. If you want to process mails with > 2GB, you're in trouble anyway
}

func parseInt(valStr string) int {
	val, err := strconv.Atoi(valStr)
	if err != nil {
		log.Fatalf("Invalid configuration file: '%s' is not a numerical value: %v", valStr, err)
	}
	return val
}
