package main

import (
	// "database/sql"
	"fmt"
	"net/http"
	"encoding/json"
	"io/ioutil"
	"time"
	"sync"

	"github.com/pkg/errors"

	"github.com/uptrace/bun"
)

const baseURL = "https://circleci.com/api/v1.1/project/github/determined-ai/determined"

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

type Artifact struct {
	bun.BaseModel

	URL       string    `json:"url"`
	BuildNum  int       `json:"build_num"`
}

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

func (c *Client) GetBuilds(limit int, offset int) ([]Build, error) {
	resp, err := c.Get(baseURL + fmt.Sprintf("?shallow=true&limit=%v&offset=%v", limit, offset))
	if err != nil {
		return nil, err
	}

	var builds []Build
	err = json.Unmarshal(resp, &builds)
	return builds, err
}

func (c *Client) GetArtifacts(buildNum int) ([]Artifact, error) {
	resp, err := c.Get(baseURL + fmt.Sprintf("/%v/artifacts", buildNum))

	var artifacts []Artifact
	err = json.Unmarshal(resp, &artifacts)
	// set the buildnum
	for i := range artifacts {
		artifacts[i].BuildNum = buildNum
	}
	return artifacts, err
}

func (c *Client) GetArtifactsWithCache(db DB, b Build) ([]Artifact, error) {
	if b.Archived {
		// use cached artifacts
		return db.GetArtifacts(b.BuildNum)
	}

	artifacts, err := c.GetArtifacts(b.BuildNum)
	if err != nil {
		return nil, err
	}

	if b.Lifecycle == "finished" {
		// cache these artifacts
		err = db.UpsertArtifacts(artifacts)
		if err != nil {
			return nil, err
		}

		err = db.ArchiveBuild(b.BuildNum)
		if err != nil {
			return nil, err
		}
	}

	return artifacts, nil
}

func (c *Client) BootstrapBuilds(db RootDB) (err error) {
	println("bootstrapping database, this may take a while...")
	var tx TxDB
	tx, err = db.Begin()
	if err != nil {
		return
	}

	var nbuilds int
	var nreported int

	defer func(){
		if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
			fmt.Printf("bootstrapping database complete, %v builds found.\n", nbuilds)
		}
		return
	}()

	// Our goal is linking to artifacts, which last for 30 days.
	boundary := time.Now().AddDate(0, 0, -30)

	for offset := 0; true; offset += 100 {
		var builds []Build
		builds, err = c.GetBuilds(100, offset)
		if err != nil {
			return
		}
		if len(builds) == 0 {
			return
		}
		for _, b := range builds {
			if !b.StartTime.After(boundary) {
				fmt.Printf(
					"Quitting after 30 days of builds; artifacts won't exist further back.\n",
				)
				return
			}
			err = tx.UpsertBuild(b)
			if err != nil {
				return
			}
			nbuilds++
		}
		if nbuilds >= nreported + 1000 {
			nreported = nbuilds
			fmt.Printf("%v builds found so far...\n", nbuilds)
		}
	}

	return nil
}

func (c *Client) RefreshBuilds(db RootDB, afterBuildNum int) (err error) {
	var tx TxDB
	tx, err = db.Begin()
	if err != nil {
		return
	}

	defer func(){
		if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
		return
	}()

	for offset := 0; true; offset += 100 {
		var builds []Build
		builds, err = c.GetBuilds(100, offset)
		if err != nil {
			return
		}
		if len(builds) == 0 {
			fmt.Printf("ran out of builds during a refresh??\n")
			return
		}
		for _, b := range builds {
			if b.BuildNum <= afterBuildNum {
				return
			}
			err = tx.UpsertBuild(b)
			if err != nil {
				return
			}
		}
	}

	return nil
}

/*
Strategy:
- on boostrap, scrape as far back as you can go for coverage artifacts
- periodically scrape as far back as the oldest non-completed build in the database
- have a force-refresh page that triggers the scrape when a user loads the page
*/

func (c *Client) StartupDB(dbPath string) (RootDB, error) {
	db, err := NewDB(dbPath)
	if err != nil {
		return db, err
	}

	err = c.RefreshDB(db, true)
	if err != nil {
		return db, err
	}

	return db, nil
}

func (c *Client) RefreshDB(db RootDB, allowBootstrap bool) error {
	oldestUnfinished, err := db.OldestUnfinishedBuild()
	if err != nil {
		return err
	}

	latestFinished, err := db.LatestFinishedBefore(oldestUnfinished)
	if err != nil {
		return err
	}

	if latestFinished == nil {
		// nothing in the database, time for a bootstrap
		if !allowBootstrap {
			return errors.New("database still needs bootstrap!")
		}
		err = c.BootstrapBuilds(db)
		if err != nil {
			return err
		}
	} else {
		err = c.RefreshBuilds(db, latestFinished.BuildNum)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) RefreshBuildsThread(db RootDB, cond *sync.Cond, ready *bool) {
	setReady := func() {
		cond.L.Lock()
		defer cond.L.Unlock()
		*ready = true
		cond.Signal()
	}

	for {
		// refresh every 15 seconds
		time.Sleep(15 * time.Second)
		fmt.Printf("refreshing builds\n")
		err := c.RefreshDB(db, false)
		if err != nil {
			fmt.Printf("error in artifact thread: %v\n", err.Error())
		}
		setReady()
	}
}

func (c *Client) RefreshArtifactsThread(db DB, cond *sync.Cond, ready *bool) {
	awaitReady := func() {
		cond.L.Lock()
		defer cond.L.Unlock()
		for !*ready {
			cond.Wait()
		}
		// "consume" the ready signal
		*ready = false
	}

	// continue archiving until we hit an error or run out of builds
	cacheMany := func() error {
		for {
			// grab the latest archivable build
			b, err := db.GetArchivableBuild()
			if err != nil {
				return err
			}

			if b == nil {
				// no cacheable artifacts found
				return nil
			}

			fmt.Printf("fetching artifacts for %v\n", b.BuildNum)
			artifacts, err := c.GetArtifacts(b.BuildNum)
			if err != nil {
				return err
			}

			// cache these artifacts
			err = db.UpsertArtifacts(artifacts)
			if err != nil {
				return err
			}

			err = db.ArchiveBuild(b.BuildNum)
			if err != nil {
				return err
			}
		}
		return nil
	}

	for {
		// wait for a signal
		awaitReady()
		err := cacheMany()
		if err != nil {
			fmt.Printf("error in artifact thread: %v\n", err.Error())
		}
	}
}
