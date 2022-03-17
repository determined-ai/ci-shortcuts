package db

import (
	"database/sql"

	"github.com/pkg/errors"

	_ "github.com/mattn/go-sqlite3"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/extra/bundebug"
)

var DB *RootDB

// a DBLike is either a *bun.DB or a *bun.Tx
type DBLike interface {
	NewInsert() *bun.InsertQuery
	NewSelect() *bun.SelectQuery
	NewUpdate() *bun.UpdateQuery
}

// dbImpl implements our OO-style database interface
// dbImpl automatically implements DBLike by composition.
type dbImpl struct {
	DBLike
}

// RootDB implements DBLike and dbImpl.
// Additionally RootDB has a RunInTx() method.
type RootDB struct {
	// bun.DB makes RootDB implement DBLike
	*bun.DB
	// dbImpl will just contain another pointer to the *bun.DB
	dbImpl
}

// RootDB.RunInTx() wraps bun.DB.RunInTx to provide the DB interface without giving access to the
// actual bun.Tx object.
/*
func (db *RootDB) RunInTx(fn func(DB) error) error {
	bunFn := func(_ context.Context, tx bun.Tx) error {
		txdb := dbImpl{&tx}
		return fn(txdb)
	}
	return db.DB.RunInTx(context.Background(), nil, bunFn)
}
*/

func NewDB(dbPath string) (RootDB, error) {
	sqldb, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return RootDB{}, err
	}

	// create the database if it doesn't exist
	// XXX this'll be in bun migrations.
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
		return RootDB{}, errors.Wrap(err, "creating tables")
	}

	db := bun.NewDB(sqldb, sqlitedialect.New())
	// db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))
	db.AddQueryHook(bundebug.NewQueryHook())
	DB = &RootDB{db, dbImpl{db}}
	return *DB, nil
}
