package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"shortcuts/ciartifact"
	"shortcuts/cibuild"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

type Server struct {
	Client Client
}

type SearchSpec struct {
	Suffix string
	Result string
}

const (
	harnessSuffix = "cov-html/harness/index.html"
	modelHubSuffix = "cov-html/model_hub/index.html"
	masterSuffix = "go-coverage/master-coverage.html"
	agentSuffix = "go-coverage/agent-coverage.html"
	webuiSuffix = "webui/react/coverage/lcov-report/index.html"
)

// findMostRecent searches artifacts in reverse chronological order looking for the most recent of
// each SearchSpec, caching any artifacts it had to pull as it goes.
func (s *Server) findMostRecent(pull int, specs ...*SearchSpec) error {
	builds, err := cibuild.GetBuildsForPull(pull)
	if err != nil {
		return nil
	}

	// sort builds by build number, most recent (larger build numbers) first
	lessFn := func(i, j int) bool {
		return builds[i].BuildNum > builds[j].BuildNum
	}
	sort.Slice(builds, lessFn)

	// find the most recent display urls for each coverage report requested
	for _, b := range builds {
		artifacts, err := s.Client.GetArtifactsWithCache(b)
		if err != nil {
			return err
		}
		for _, a := range artifacts {
			// check if this matches any of our specs
			for i := 0; i < len(specs); i++ {
				if !strings.HasSuffix(a.URL, specs[i].Suffix) {
					continue
				}
				// we have a match!
				specs[i].Result = a.URL
				// remove this spec from our list of specs
				last := len(specs)-1
				if i < last {
					specs[i] = specs[last]
				}
				specs = specs[:last]
				// specs[i] changed, so re-evaluate it
				i--;
			}
			if len(specs) == 0 {
				return nil
			}
		}
	}
	return nil
}

func (s *Server) getRedirect(c echo.Context, suffix, frag string) error {
	pull, err := strconv.Atoi(c.Param("pull"))
	if err != nil {
		return c.String(http.StatusOK, err.Error())
	}

	spec := SearchSpec{Suffix: suffix}
	err = s.findMostRecent(pull, &spec)
	if err != nil {
		return c.String(http.StatusOK, err.Error())
	}

	if spec.Result == "" {
		return c.HTML(
			http.StatusOK,
			fmt.Sprintf(`<font color="gray">coverage report not ready</font><br>`),
		)
	}

	return c.Redirect(http.StatusTemporaryRedirect, spec.Result + frag)
}

// GET /pull/:pull/harness-coverage
func (s *Server) getHarness(c echo.Context) error {
	return s.getRedirect(c, harnessSuffix, "")
}

// GET /pull/:pull/model-hub-coverage
func (s *Server) getModelHub(c echo.Context) error {
	return s.getRedirect(c, modelHubSuffix, "")
}

// GET /pull/:pull/master-coverage
func (s *Server) getMaster(c echo.Context) error {
	return s.getRedirect(c, masterSuffix, "#file0")
}

// GET /pull/:pull/agent-coverage
func (s *Server) getAgent(c echo.Context) error {
	return s.getRedirect(c, agentSuffix, "#file0")
}

// GET /pull/:pull/webui-coverage
func (s *Server) getWebui(c echo.Context) error {
	return s.getRedirect(c, webuiSuffix, "")
}

// GET /pull/:pull
func (s *Server) getPull(c echo.Context) error {
	pull, err := strconv.Atoi(c.Param("pull"))
	if err != nil {
		return c.String(http.StatusOK, err.Error())
	}

	// find the most recent display urls for each coverage report
	harness := SearchSpec{Suffix: harnessSuffix}
	modelHub := SearchSpec{Suffix: modelHubSuffix}
	master := SearchSpec{Suffix: masterSuffix}
	agent := SearchSpec{Suffix: agentSuffix}
	webui := SearchSpec{Suffix: webuiSuffix}

	err = s.findMostRecent(pull, &harness, &modelHub, &master, &agent, &webui)
	if err != nil {
		return c.String(http.StatusOK, err.Error())
	}

	h := strings.Builder{}
	_, _ = h.WriteString(`<html>coverage reports for <a href="https://github.com/`)
	_, _ = h.WriteString(
		fmt.Sprintf(`determined-ai/determined/pull/%v">pull/%v</a>:<br>`, pull, pull),
	)

	showURL := func(name, val, frag string) {
		if val == "" {
			_, _ = h.WriteString(
				fmt.Sprintf(`<font color="gray">%v coverage (not ready)</font><br>`, name),
			)
		} else {
			_, _ = h.WriteString(fmt.Sprintf(`<a href="%v%v">%v coverage</a><br>`, val, frag, name))
		}
	}

	showURL("harness", harness.Result, "")
	showURL("model_hub", modelHub.Result, "")
	showURL("master", master.Result, "#file0")
	showURL("agent", agent.Result, "#file0")
	showURL("webui", webui.Result, "")

	return c.HTML(http.StatusOK, h.String())
}


func RefreshBuildsThread(client *Client, ready chan<- struct{}) {
	// Every 15 seconds, scrape as far back as the oldest non-completed build in the database.
	for {
		time.Sleep(15 * time.Second)
		fmt.Printf("refreshing builds\n")
		err := client.RefreshDB(false)
		if err != nil {
			fmt.Printf("error in artifact thread: %v\n", err.Error())
		}
		// Send a signal to the RefreshBuildsThread if it is waiting.
		select {
			case ready <- struct{}{}:
			default:
		}
	}
}

func RefreshArtifactsThread(client *Client, ready <-chan struct{}) {
	// Continuously scrape artifacts for finished builds that are not yet archived.
	// If you run out of finish builds, we pause until the RefreshBuildsThread wakes us up.

	// continue archiving until we hit an error or run out of builds
	cacheMany := func() error {
		for {
			// grab the latest archivable build
			b, err := cibuild.GetArchivableBuild()
			if err != nil {
				return err
			}

			if b == nil {
				// no cacheable artifacts found
				return nil
			}

			fmt.Printf("fetching artifacts for %v\n", b.BuildNum)
			artifacts, err := client.GetArtifacts(b.BuildNum)
			if err != nil {
				return err
			}

			// cache these artifacts
			err = ciartifact.UpsertArtifacts(artifacts)
			if err != nil {
				return err
			}

			err = cibuild.ArchiveBuild(b.BuildNum)
			if err != nil {
				return err
			}
		}
		return nil
	}

	for {
		err := cacheMany()
		if err != nil {
			fmt.Printf("error in artifact thread: %v\n", err.Error())
		}
		// wait for a signal from the RefreshBuildsThread
		_ = <-ready
	}
}

func main() {
	if len(os.Args) < 3 {
		fmt.Printf("usage: shortcuts SQLITE_PATH SERVER_SPEC\n")
		fmt.Printf("example: shortcuts sqlite.db :5729\n")
		os.Exit(1)
	}

	dbPath := os.Args[1]
	srvSpec := os.Args[2]

	client := Client{&http.Client{}}

	_, err := client.StartupDB(dbPath)
	if err != nil {
		log.Fatal(err)
	}

	srv := Server{client}

	ready := make(chan struct{})

	go RefreshArtifactsThread(&client, ready)
	go RefreshBuildsThread(&client, ready)

	e := echo.New()
	e.GET("/pull/:pull", srv.getPull)
	e.GET("/pull/:pull/harness-coverage", srv.getHarness)
	e.GET("/pull/:pull/model-hub-coverage", srv.getModelHub)
	e.GET("/pull/:pull/master-coverage", srv.getMaster)
	e.GET("/pull/:pull/agent-coverage", srv.getAgent)
	e.GET("/pull/:pull/webui-coverage", srv.getWebui)
	e.Logger.Fatal(e.Start(srvSpec))
}
