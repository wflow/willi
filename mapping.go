package main

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

var ErrNoUpstreamFound = errors.New("No server found for key")

type Upstream struct {
	Server    string
	TlsVerify bool
}

func (u *Upstream) String() string {
	verified := "tls unverified"
	if u.TlsVerify {
		verified = "tls verified"
	}
	return fmt.Sprintf("{%s, %s}", u.Server, verified)
}

type Mapping interface {
	Get(key string) (Upstream, error)
}

type staticMapping struct {
	server Upstream
}

func NewStaticMapping(server string, tlsVerify bool) (Mapping, error) {
	return &staticMapping{
		server: Upstream{
			Server:    server,
			TlsVerify: tlsVerify,
		}}, nil
}

func (m *staticMapping) Get(key string) (Upstream, error) {
	return m.server, nil
}

func (m *staticMapping) String() string {
	return fmt.Sprintf("{static, %s}", &m.server)
}

type csvMapping struct {
	servers map[string]Upstream
}

func NewCSVMapping(filename string) (Mapping, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	mapping := &csvMapping{
		servers: make(map[string]Upstream, 0),
	}

	r := csv.NewReader(f)
	r.Comma = ';'
	r.Comment = '#'
	r.FieldsPerRecord = -1 // allow variable number of fields

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
		key := strings.TrimSpace(record[0])
		server := strings.TrimSpace(record[1])

		t := "true"
		if len(record) > 2 {
			t = strings.TrimSpace(record[2])
		}

		tlsVerify, err := strconv.ParseBool(t)
		if err != nil {
			return nil, fmt.Errorf("tls_verify: %w", err)
		}

		mapping.servers[key] = Upstream{
			Server:    server,
			TlsVerify: tlsVerify,
		}
	}

	return mapping, nil
}

func (m *csvMapping) Get(key string) (Upstream, error) {
	if server, ok := m.servers[key]; ok {
		return server, nil
	}

	return Upstream{}, ErrNoUpstreamFound
}

func (m *csvMapping) String() string {
	return fmt.Sprintf("{csv, %d entries}", len(m.servers))
}

type sqlMapping struct {
	driverName  string
	redactedDsn string
	query       string

	db *sqlx.DB
}

func NewSQLMapping(driverName string, dsn string, query string) (Mapping, error) {
	db, err := sqlx.Open(driverName, dsn)
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

type dbbool bool

func (b *dbbool) Scan(src interface{}) error {
	v, ok := src.(string)
	if !ok {
		return fmt.Errorf("expected 'true' or 'false' but got %T", src)
	}

	switch strings.ToLower(v) {
	case "true":
		*b = true
	case "false":
		*b = false
	default:
		return fmt.Errorf("expected 'true' or 'false' but got '%s'", v)
	}

	return nil
}

func (m *sqlMapping) Get(key string) (Upstream, error) {
	res := m.db.QueryRowx(m.query, key)

	row := struct {
		Server    string `db:"server"`
		TlsVerify dbbool `db:"tls_verify"`
	}{
		Server:    "",
		TlsVerify: dbbool(true),
	}

	err := res.StructScan(&row)
	if err == sql.ErrNoRows {
		return Upstream{}, ErrNoUpstreamFound
	}
	if err != nil {
		return Upstream{}, err
	}

	return Upstream{
		Server:    row.Server,
		TlsVerify: bool(row.TlsVerify),
	}, nil
}

func (m *sqlMapping) String() string {
	return fmt.Sprintf("{%s, %s, '%s'}", m.driverName, m.redactedDsn, m.query)
}
