package main

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

var ErrNoServerFound = errors.New("No server found for pattern")

type Mapping interface {
	GetServer(pattern string) (string, error)
}

type staticMapping struct {
	server string
}

func NewStaticMapping(server string) (Mapping, error) {
	r := regexp.MustCompile("^[a-zA-Z0-9-.]+:[0-9]+$")
	if !r.MatchString(server) {
		return nil, fmt.Errorf("server must be in format <hostname>:<port>")
	}

	return &staticMapping{server: server}, nil
}

func (m *staticMapping) GetServer(pattern string) (string, error) {
	return fmt.Sprintf("%s", m.server), nil
}

func (m *staticMapping) String() string {
	return fmt.Sprintf("{static, %s}", m.server)
}

type csvMapping struct {
	servers map[string]string
}

func NewCSVMapping(filename string) (Mapping, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	mapping := &csvMapping{
		servers: make(map[string]string, 0),
	}

	r := csv.NewReader(f)
	r.Comma = ';'
	r.Comment = '#'

	// skip first line (column headers)
	if _, err := r.Read(); err != nil {
		return nil, err
	}

	// read the rest
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	for _, record := range records {
		pattern := strings.TrimSpace(record[0])
		server := strings.TrimSpace(record[1])

		mapping.servers[pattern] = server
	}

	return mapping, nil
}

func (m *csvMapping) GetServer(pattern string) (string, error) {
	if server, ok := m.servers[pattern]; ok {
		return server, nil
	}

	return "", ErrNoServerFound
}

func (m *csvMapping) String() string {
	return fmt.Sprintf("{csv, %d entries}", len(m.servers))
}

type sqlMapping struct {
	driverName  string
	redactedDsn string
	query       string

	db *sql.DB
}

func NewSQLMapping(driverName string, dsn string, query string) (Mapping, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}

	r := regexp.MustCompile("^(.+):(.+)@(.+)$")
	m := r.FindStringSubmatch(dsn)
	if len(m) == 4 {
		dsn = fmt.Sprintf("%s:<redacted>@%s", m[1], m[3])
	}

	return &sqlMapping{driverName, dsn, query, db}, nil
}

func (m *sqlMapping) GetServer(pattern string) (string, error) {
	var server string

	res := m.db.QueryRow(m.query, pattern)

	err := res.Scan(&server)
	if err == sql.ErrNoRows {
		return "", ErrNoServerFound
	}
	if err != nil {
		return "", err
	}

	return server, nil
}

func (m *sqlMapping) String() string {
	return fmt.Sprintf("{%s, %s, '%s'}", m.driverName, m.redactedDsn, m.query)
}
