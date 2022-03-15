package main

import (
	"os"
	"net/http"
	"strconv"
	"log"
	"sync"
	"sort"
	"strings"
	"fmt"

	"github.com/labstack/echo/v4"
)

type Server struct {
	DB RootDB
	Client Client
}

// POST /pull/:pull
func (s *Server) getPull(c echo.Context) error {
	pull, err := strconv.Atoi(c.Param("pull"))
	if err != nil {
		return c.String(http.StatusOK, err.Error())
	}

	builds, err := s.DB.GetBuildsForPull(pull)
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
		artifacts, err := s.Client.GetArtifactsWithCache(s.DB, b)
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

func main() {
	if len(os.Args) < 3 {
		fmt.Printf("usage: shortcuts SQLITE_PATH SERVER_SPEC\n")
		fmt.Printf("example: shortcuts sqlite.db :80\n")
		os.Exit(1)
	}

	client := Client{&http.Client{}}

	dbPath := os.Args[1]
	srvSpec := os.Args[2]

	db, err := client.StartupDB(dbPath)
	if err != nil {
		log.Fatal(err)
	}

	srv := Server{db, client}

	ready := true
	mutex := sync.Mutex{}
	cond := sync.NewCond(&mutex)

	go client.RefreshArtifactsThread(db, cond, &ready)
	go client.RefreshBuildsThread(db, cond, &ready)

	e := echo.New()
	e.GET("/pull/:pull", srv.getPull)
	e.Logger.Fatal(e.Start(srvSpec))
}
