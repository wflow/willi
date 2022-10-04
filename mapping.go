package main

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

type ServerMap interface {
	GetServer(pattern string) (string, error)
}

var ErrNotFound = errors.New("No host found for pattern")

// pattern;host;port
type CSVServerMap struct {
	servers map[string]string
}

func NewCSVServerMap(filename string) (*CSVServerMap, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	csvMap := &CSVServerMap{
		servers: make(map[string]string, 128),
	}

	r := csv.NewReader(f)
	r.Comma = ';'

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
		host := strings.TrimSpace(record[1])
		port := strings.TrimSpace(record[2])

		csvMap.servers[pattern] = fmt.Sprintf("%s:%s", host, port)
	}

	return csvMap, nil
}

func (m *CSVServerMap) GetServer(pattern string) (string, error) {
	if server, ok := m.servers[pattern]; ok {
		return server, nil
	}

	return "", ErrNotFound
}

type MySQLServerMap struct {
	db    *sql.DB
	query string
}

func NewMySQLServerMap(dsn string, query string) (*MySQLServerMap, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}

	return &MySQLServerMap{db, query}, nil
}

func (m *MySQLServerMap) GetServer(pattern string) (string, error) {
	var host string
	var port int

	res := m.db.QueryRow(m.query, pattern)

	err := res.Scan(&host, &port)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s:%d", host, port), nil
}

type StaticServerMap struct {
	server string
	port   int
}

func NewStaticServerMap(server string, port int) (*StaticServerMap, error) {
	return &StaticServerMap{server: server, port: port}, nil
}

func (m *StaticServerMap) GetServer(pattern string) (string, error) {
	return fmt.Sprintf("%s:%d", m.server, m.port), nil
}
