package store

import (
	"database/sql"
	"time"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) DB() *sql.DB { return s.db }

func now() int64 { return time.Now().Unix() }
