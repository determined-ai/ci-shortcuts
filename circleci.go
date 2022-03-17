package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"shortcuts/ciartifact"
	"shortcuts/cibuild"
	"shortcuts/db"
	"time"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

const baseURL = "https://circleci.com/api/v1.1/project/github/determined-ai/determined"

type Client struct {
	*http.Client
}

func (c *Client) Get(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Accept", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	return ioutil.ReadAll(resp.Body)
}

func (c *Client) GetBuilds(limit int, offset int) ([]cibuild.Build, error) {
	resp, err := c.Get(baseURL + fmt.Sprintf("?shallow=true&limit=%v&offset=%v", limit, offset))
	if err != nil {
		return nil, err
	}

	var builds []cibuild.Build
	err = json.Unmarshal(resp, &builds)
	return builds, err
}

func (c *Client) GetArtifacts(buildNum int) ([]ciartifact.Artifact, error) {
	resp, err := c.Get(baseURL + fmt.Sprintf("/%v/artifacts", buildNum))

	var artifacts []ciartifact.Artifact
	err = json.Unmarshal(resp, &artifacts)
	// set the buildnum
	for i := range artifacts {
		artifacts[i].BuildNum = buildNum
	}
	return artifacts, err
}

func (c *Client) GetArtifactsWithCache(b cibuild.Build) ([]ciartifact.Artifact, error) {
	if b.Archived {
		// use cached artifacts
		return ciartifact.GetArtifacts(b.BuildNum)
	}

	artifacts, err := c.GetArtifacts(b.BuildNum)
	if err != nil {
		return nil, err
	}

	if b.Outcome != nil {
		// cache these artifacts
		err = ciartifact.UpsertArtifacts(artifacts)
		if err != nil {
			return nil, err
		}

		err = cibuild.ArchiveBuild(b.BuildNum)
		if err != nil {
			return nil, err
		}
	}

	return artifacts, nil
}

func (c *Client) BootstrapBuilds() error {
	return db.DB.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		println("bootstrapping database, this may take a while...")
		var nbuilds int
		var nreported int

		// Our goal is linking to artifacts, which last for 30 days.
		boundary := time.Now().AddDate(0, 0, -30)

		offset := 0
		for {
			var builds []cibuild.Build
			builds, err := c.GetBuilds(100, offset)
			if err != nil {
				return err
			}
			if len(builds) == 0 {
				break
			}
			for _, b := range builds {
				if !b.StartTime.After(boundary) {
					fmt.Printf(
						"Quitting after 30 days of builds; artifacts won't exist further back.\n",
					)
					goto report
				}
				err = b.UpsertBuildTx(ctx, tx)
				if err != nil {
					return err
				}
				nbuilds++
			}
			if nbuilds >= nreported+1000 {
				nreported = nbuilds
				fmt.Printf("%v builds found so far...\n", nbuilds)
			}
			offset += len(builds)
		}

	report:
		fmt.Printf("bootstrapping database complete, %v builds found.\n", nbuilds)
		return nil
	})
}

func (c *Client) RefreshBuilds(afterBuildNum int) error {
	return db.DB.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		offset := 0
		for {
			builds, err := c.GetBuilds(100, offset)
			if err != nil {
				return err
			}
			if len(builds) == 0 {
				return errors.New("ran out of builds during a refresh??")
			}
			for _, b := range builds {
				if b.BuildNum <= afterBuildNum {
					return nil
				}
				err = b.UpsertBuildTx(ctx, tx)
				if err != nil {
					return err
				}
			}
			offset += len(builds)
		}
		return nil
	})
}

func (c *Client) StartupDB(dbPath string) (*bun.DB, error) {
	db, err := db.NewDB(dbPath)
	if err != nil {
		return db, err
	}

	err = c.RefreshDB(true)
	if err != nil {
		return db, err
	}

	return db, nil
}

func (c *Client) RefreshDB(allowBootstrap bool) error {
	oldestUnfinished, err := cibuild.OldestUnfinishedBuild()
	if err != nil {
		return err
	}

	latestFinished, err := oldestUnfinished.LatestFinishedBefore()
	if err != nil {
		return err
	}

	if latestFinished == nil {
		// nothing in the database, time for a bootstrap
		if !allowBootstrap {
			return errors.New("database still needs bootstrap!")
		}
		err = c.BootstrapBuilds()
		if err != nil {
			return err
		}
	} else {
		err = c.RefreshBuilds(latestFinished.BuildNum)
		if err != nil {
			return err
		}
	}

	return nil
}
