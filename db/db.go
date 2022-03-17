package db

import (
	"database/sql"

	"github.com/pkg/errors"

	_ "github.com/mattn/go-sqlite3"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/extra/bundebug"
)

var DB *bun.DB

func NewDB(dbPath string) (*bun.DB, error) {
	sqldb, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// create the database if it doesn't exist
	_, err = sqldb.Exec(`
		CREATE TABLE IF NOT EXISTS builds (
			build_num INTEGER PRIMARY KEY,
			url TEXT NOT NULL,
			branch TEXT NOT NULL,
			subject TEXT NOT NULL,
			'commit' TEXT NOT NULL,
			parallel INTEGER NOT NULL,
			workflow TEXT,
			start_time TEXT NOT NULL,
			lifecycle TEXT NOT NULL,
			-- this is our own metadata
			archived BOOLEAN NOT NULL DEFAULT FALSE
		);
		CREATE TABLE IF NOT EXISTS artifacts (
			url TEXT PRIMARY KEY,
			build_num INTEGER NOT NULL
		);
	`)
	if err != nil {
		sqldb.Close()
		return nil, errors.Wrap(err, "creating tables")
	}

	db := bun.NewDB(sqldb, sqlitedialect.New())
	// db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))
	db.AddQueryHook(bundebug.NewQueryHook())
	DB = db
	return DB, nil
}
