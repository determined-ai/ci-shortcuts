package cibuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"shortcuts/db"
	"time"

	"github.com/uptrace/bun"
)

type Workflow string

func (w *Workflow) UnmarshalJSON(b []byte) error {
	var tmp struct {
		Workflow string `json:"workflow_name"`
	}
	err := json.Unmarshal(b, &tmp)
	if err != nil {
		return err
	}
	*w = Workflow(tmp.Workflow)
	return nil
}

type Build struct {
	bun.BaseModel

	BuildNum  int       `json:"build_num" bun:",pk"`
	Lifecycle string    `json:"lifecycle"`
	URL       string    `json:"build_url"`
	Subject   string    `json:"subject"`
	Branch    string    `json:"branch"`
	Commit    string    `json:"vcs_revision"`
	Parallel  int       `json:"parallel"`
	Workflow  Workflow  `json:"workflows"`
	StartTime time.Time `json:"start_time"`

	// Archived means we checked for artifacts after it finished (no more can be added)
	Archived bool
}

func (b *Build) UpsertBuildTx(ctx context.Context, db bun.IDB) error {
	q := db.NewInsert().
		Model(&b).
		ExcludeColumn("archived").
		On("CONFLICT (build_num) DO UPDATE").
		Set("lifecycle = EXCLUDED.lifecycle")
	_, err := q.Exec(ctx)
	return err
}

func (b *Build) UpsertBuild() error {
	return b.UpsertBuildTx(context.Background(), db.DB)
}

func GetBuild(buildNum int) (*Build, error) {
	b := Build{BuildNum: buildNum}
	q := db.DB.NewSelect().
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

func GetBuildsForPull(pull int) ([]Build, error) {
	var builds []Build
	q := db.DB.NewSelect()
	q = q.Model(&builds)
	q = q.Where("branch = ?", fmt.Sprintf("pull/%v", pull))
	err := q.Scan(context.Background())
	return builds, err
}

func OldestUnfinishedBuild() (*Build, error) {
	var b Build
	q := db.DB.NewSelect().
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

func (before *Build) LatestFinishedBefore() (*Build, error) {
	var b Build
	q := db.DB.NewSelect().
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

func GetArchivableBuild() (*Build, error) {
	var b Build
	q := db.DB.NewSelect().
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

func UnarchivedBuilds() ([]Build, error) {
	var builds []Build
	q := db.DB.NewSelect().
		Model(&builds).
		Where("archived = false")
	err := q.Scan(context.Background())
	return builds, err
}

func ArchiveBuild(buildNum int) error {
	b := Build{BuildNum: buildNum, Archived: true}
	q := db.DB.NewUpdate().
		Model(&b).
		Column("archived").
		WherePK()
	_, err := q.Exec(context.Background())
	return err
}
