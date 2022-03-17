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
	_, err := db.DB.NewInsert().
		Model(&artifacts).
		Ignore().
		Exec(context.Background())
	return err
}

func GetArtifacts(buildNum int) ([]Artifact, error) {
	var artifacts []Artifact
	err := db.DB.NewSelect().
		Model(&artifacts).
		Where("build_num = ?", buildNum).
		Scan(context.Background())
	return artifacts, err
}
