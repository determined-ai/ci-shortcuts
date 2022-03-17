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

// POST /pull/:pull
func (s *Server) getPull(c echo.Context) error {
	pull, err := strconv.Atoi(c.Param("pull"))
	if err != nil {
		return c.String(http.StatusOK, err.Error())
	}

	builds, err := cibuild.GetBuildsForPull(pull)
	if err != nil {
		return c.String(http.StatusOK, err.Error())
	}

	// sort builds by build number, most recent (larger build numbers) first
	lessFn := func(i, j int) bool {
		return builds[i].BuildNum > builds[j].BuildNum
	}
	sort.Slice(builds, lessFn)

	// find the most recent display urls for each coverage report
	var harness string
	var modelHub string
	var master string
	var agent string
	var webui string

	for _, b := range builds {
		artifacts, err := s.Client.GetArtifactsWithCache(b)
		if err != nil {
			return c.String(http.StatusOK, err.Error())
		}
		for _, a := range artifacts {
			if harness == "" && strings.HasSuffix(a.URL, "cov-html/harness/index.html") {
				harness = a.URL
			}
			if modelHub == "" && strings.HasSuffix(a.URL, "cov-html/model_hub/index.html") {
				modelHub = a.URL
			}
			if master == "" && strings.HasSuffix(a.URL, "go-coverage/master-coverage.html") {
				master = a.URL
			}
			if agent == "" && strings.HasSuffix(a.URL, "go-coverage/agent-coverage.html") {
				agent = a.URL
			}
			webuiSuffix := "webui/react/coverage/lcov-report/index.html"
			if webui == "" && strings.HasSuffix(a.URL, webuiSuffix) {
				webui = a.URL
			}
		}
		if harness != "" && modelHub != "" && master != "" && agent != "" && webui != "" {
			break
		}
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

	showURL("harness", harness, "")
	showURL("model_hub", modelHub, "")
	showURL("master", master, "#file0")
	showURL("agent", agent, "#file0")
	showURL("webui", webui, "")

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
		fmt.Printf("example: shortcuts sqlite.db :80\n")
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
	e.Logger.Fatal(e.Start(srvSpec))
}
