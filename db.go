package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pkg/errors"

	_ "github.com/mattn/go-sqlite3"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/extra/bundebug"
	"github.com/uptrace/bun/dialect/sqlitedialect"

)

// a DBLike is either a *bun.DB or a *bun.Tx
type DBLike interface {
	NewInsert() *bun.InsertQuery
	NewSelect() *bun.SelectQuery
	NewUpdate() *bun.UpdateQuery
}

// DB is our OO-style database interface
// Both RootDB and TxDB implement this, but:
//  - RootDB has a .Begin() method that returns a TxDB
//  - TxDB has .Commit() and .Rollback() methods
type DB interface {
	UpsertBuild(b Build) error
	GetBuild(buildNum int) (*Build, error)
	GetBuildsForPull(pull int) ([]Build, error)
	OldestUnfinishedBuild() (*Build, error)
	LatestFinishedBefore(before *Build) (*Build, error)
	GetArchivableBuild() (*Build, error)
	UnarchivedBuilds() ([]Build, error)
	ArchiveBuild(buildNum int) error
	UpsertArtifacts(artifacts []Artifact) error
	GetArtifacts(buildNum int) ([]Artifact, error)
}


// dbImpl implements our OO-style database interface
// dbImpl automatically implements DBLike by composition.
type dbImpl struct {
	DBLike
}

// RootDB implements DBLike and dbImpl.
// Additionally RootDB has a Begin() method.
type RootDB struct {
	// bun.DB makes RootDB implement DBLike
	*bun.DB
	// dbImpl will just contain another pointer to the *bun.DB
	dbImpl
}

// TxDB provides the same OO-style database, plus the usual bun.Tx functions
// TxDB implements DBLike and dbImple.
// Additionally it has .Rollback() and .Commit() mechanisms offered by bun.Tx.
type TxDB struct {
	*bun.Tx
	// dbImpl will just contain another pointer to the *bun.Tx
	dbImpl
}

// RootDB.Begin() returns a bun.Tx wrapped in a TxDB.
func (db *RootDB) Begin() (TxDB, error)  {
	tx, err := db.DB.Begin()
	return TxDB{&tx, dbImpl{&tx}}, err
}

func NewDB(dbPath string) (RootDB, error) {
	sqldb, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return RootDB{}, err
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
		return RootDB{}, errors.Wrap(err, "creating tables")
	}

	db := bun.NewDB(sqldb, sqlitedialect.New())
	// db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))
	db.AddQueryHook(bundebug.NewQueryHook())
	return RootDB{db, dbImpl{db}}, nil
}

func (db dbImpl) UpsertBuild(b Build) error {
	q := db.NewInsert().
		Model(&b).
		ExcludeColumn("archived").
		On("CONFLICT (build_num) DO UPDATE").
		Set("lifecycle = EXCLUDED.lifecycle")
	_, err := q.Exec(context.Background())
	return err
}

func (db dbImpl) GetBuild(buildNum int) (*Build, error) {
	b := Build{BuildNum: buildNum}
	q := db.NewSelect().
		Model(&b).
		WherePK()
	err := q.Scan(context.Background())
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (db dbImpl) GetBuildsForPull(pull int) ([]Build, error) {
	var builds []Build
	q := db.NewSelect()
	q = q.Model(&builds)
	q = q.Where("branch = ?", fmt.Sprintf("pull/%v", pull))
	err := q.Scan(context.Background())
	return builds, err
}

func (db dbImpl) OldestUnfinishedBuild() (*Build, error) {
	var b Build
	q := db.NewSelect().
		Model(&b).
		Where("lifecycle != ?", "finished").
		Order("build_num ASC").
		Limit(1)
	err := q.Scan(context.Background())
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (db dbImpl) LatestFinishedBefore(before *Build) (*Build, error) {
	var b Build
	q := db.NewSelect().
		Model(&b).
		Where("lifecycle == ?", "finished").
		Order("build_num DESC").
		Limit(1)
	if before != nil {
		q = q.Where("build_num < ?", before.BuildNum)
	}
	err := q.Scan(context.Background())
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (db dbImpl) GetArchivableBuild() (*Build, error) {
	var b Build
	q := db.NewSelect().
		Model(&b).
		Where("lifecycle == ?", "finished").
		Where("archived == false").
		Order("build_num DESC").
		Limit(1)
	err := q.Scan(context.Background())
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (db dbImpl) UnarchivedBuilds() ([]Build, error) {
	var builds []Build
	q := db.NewSelect().
		Model(&builds).
		Where("archived = false")
	err := q.Scan(context.Background())
	return builds, err
}

func (db dbImpl) ArchiveBuild(buildNum int) error {
	b := Build{BuildNum: buildNum, Archived: true}
	q := db.NewUpdate().
		Model(&b).
		Column("archived").
		WherePK()
	_, err := q.Exec(context.Background())
	return err
}

func (db dbImpl) UpsertArtifacts(artifacts []Artifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	q := db.NewInsert().
		Model(&artifacts).
		Ignore()
	_, err := q.Exec(context.Background())
	return err
}

func (db dbImpl) GetArtifacts(buildNum int) ([]Artifact, error) {
	var artifacts []Artifact
	q := db.NewSelect().
		Model(&artifacts).
		Where("build_num = ?", buildNum)
	err := q.Scan(context.Background())
	return artifacts, err
}
