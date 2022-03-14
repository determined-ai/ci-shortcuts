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

type DB struct {
	*bun.DB
}

func NewDB(dbPath string) (DB, error) {
	sqldb, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return DB{}, err
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
		return DB{}, errors.Wrap(err, "creating tables")
	}

	db := bun.NewDB(sqldb, sqlitedialect.New())
	// db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))
	db.AddQueryHook(bundebug.NewQueryHook())
	return DB{db}, nil
}

type DBLike interface {
	NewInsert() *bun.InsertQuery
	NewSelect() *bun.SelectQuery
	NewUpdate() *bun.UpdateQuery
}

func UpsertBuild(db DBLike, b Build) error {
	q := db.NewInsert()
	q = q.Model(&b)
	q = q.ExcludeColumn("archived")
	q = q.On("CONFLICT (build_num) DO UPDATE")
	q = q.Set("lifecycle = EXCLUDED.lifecycle")
	_, err := q.Exec(context.Background())
	return err
}

func GetBuild(db DBLike, buildNum int) (*Build, error) {
	b := Build{BuildNum: buildNum}
	q := db.NewSelect()
	q = q.Model(&b)
	q = q.WherePK()
	err := q.Scan(context.Background())
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func GetBuildsForPull(db DBLike, pull int) ([]Build, error) {
	var builds []Build
	q := db.NewSelect()
	q = q.Model(&builds)
	q = q.Where("branch = ?", fmt.Sprintf("pull/%v", pull))
	err := q.Scan(context.Background())
	return builds, err
}

func OldestUnfinishedBuild(db DBLike) (*Build, error) {
	var b Build
	q := db.NewSelect()
	q = q.Model(&b)
	q = q.Where("lifecycle != ?", "finished")
	q = q.Order("build_num ASC")
	q = q.Limit(1)
	err := q.Scan(context.Background())
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func LatestFinishedBefore(db DBLike, before *Build) (*Build, error) {
	var b Build
	q := db.NewSelect()
	q = q.Model(&b)
	q = q.Where("lifecycle == ?", "finished")
	if before != nil {
		q.Where("build_num < ?", before.BuildNum)
	}
	q = q.Order("build_num DESC")
	q = q.Limit(1)
	err := q.Scan(context.Background())
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func GetArchivableBuild(db DBLike) (*Build, error) {
	var b Build
	q := db.NewSelect()
	q = q.Model(&b)
	q = q.Where("lifecycle == ?", "finished")
	q = q.Where("archived == false")
	q = q.Order("build_num DESC")
	q = q.Limit(1)
	err := q.Scan(context.Background())
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func UnarchivedBuilds(db DBLike) ([]Build, error) {
	var builds []Build
	q := db.NewSelect()
	q = q.Model(&builds)
	q = q.Where("archived = false")
	err := q.Scan(context.Background())
	return builds, err
}

func ArchiveBuild(db DBLike, buildNum int) error {
	b := Build{BuildNum: buildNum, Archived: true}
	q := db.NewUpdate()
	q = q.Model(&b)
	q = q.Column("archived")
	q = q.WherePK()
	_, err := q.Exec(context.Background())
	return err
}

func UpsertArtifacts(db DBLike, artifacts []Artifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	q := db.NewInsert()
	q = q.Model(&artifacts)
	q = q.Ignore()
	_, err := q.Exec(context.Background())
	return err
}

func GetArtifacts(db DBLike, buildNum int) ([]Artifact, error) {
	var artifacts []Artifact
	q := db.NewSelect()
	q = q.Model(&artifacts)
	q = q.Where("build_num = ?", buildNum)
	err := q.Scan(context.Background())
	return artifacts, err
}
