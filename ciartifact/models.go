package ciartifact

import (
	"context"
	"shortcuts/db"

	"github.com/uptrace/bun"
)

type Artifact struct {
	bun.BaseModel

	URL      string `json:"url"`
	BuildNum int    `json:"build_num"`
}

func UpsertArtifacts(artifacts []Artifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	q := db.DB.NewInsert().
		Model(&artifacts).
		Ignore()
	_, err := q.Exec(context.Background())
	return err
}

func GetArtifacts(buildNum int) ([]Artifact, error) {
	var artifacts []Artifact
	q := db.DB.NewSelect().
		Model(&artifacts).
		Where("build_num = ?", buildNum)
	err := q.Scan(context.Background())
	return artifacts, err
}
