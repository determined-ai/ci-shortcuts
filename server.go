package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"shortcuts/ciartifact"
	"shortcuts/cibuild"
	"shortcuts/github"
)

type Server struct {
	Client Client
	Github github.Github
}

type SearchSpec struct {
	Suffix string
	Result string
}

const (
	agentSuffix = "go-coverage/agent-coverage.html"
	harnessSuffix = "cov-html/harness/index.html"
	masterSuffix = "go-coverage/master-coverage.html"
	modelHubSuffix = "cov-html/model_hub/index.html"
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

// GET /pull/:pull/agent-coverage
func (s *Server) getAgent(c echo.Context) error {
	return s.getRedirect(c, agentSuffix, "#file0")
}

// GET /pull/:pull/harness-coverage
func (s *Server) getHarness(c echo.Context) error {
	return s.getRedirect(c, harnessSuffix, "")
}

// GET /pull/:pull/master-coverage
func (s *Server) getMaster(c echo.Context) error {
	return s.getRedirect(c, masterSuffix, "#file0")
}

// GET /pull/:pull/model-hub-coverage
func (s *Server) getModelHub(c echo.Context) error {
	return s.getRedirect(c, modelHubSuffix, "")
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

	showURL("agent", agent.Result, "#file0")
	showURL("harness", harness.Result, "")
	showURL("master", master.Result, "#file0")
	showURL("model_hub", modelHub.Result, "")
	showURL("webui", webui.Result, "")

	return c.HTML(http.StatusOK, h.String())
}

func (s *Server) postShortcutComment(installationID int64, prNumber int) error {
	// POST a comment with the shortcuts:
	//
	//    Shortcuts to the latest built artifacts for:
	//    - all coverage reports (agent, harness, master, model_hub, webui)
	//
	// Note we put line breaks within the hyperlink syntax to "hide" them in the rendered comment.
	msg := fmt.Sprintf(`Shortcuts to the latest built artifacts for:
- [all coverage reports](https://det-ci.dzhu.dev/shortcuts/pull/%[1]v
) ([agent](https://det-ci.dzhu.dev/shortcuts/pull/%[1]v/agent-coverage
), [harness](https://det-ci.dzhu.dev/shortcuts/pull/%[1]v/harness-coverage
), [master](https://det-ci.dzhu.dev/shortcuts/pull/%[1]v/master-coverage
), [model_hub](https://det-ci.dzhu.dev/shortcuts/pull/%[1]v/model-hub-coverage
), [webui](https://det-ci.dzhu.dev/shortcuts/pull/%[1]v/webui-coverage))`, prNumber)
	body := map[string]interface{}{"body": msg}
	url := fmt.Sprintf(
		"https://api.github.com/repos/determined-ai/determined/issues/%v/comments", prNumber,
	)
	resp, err := s.Github.Post(installationID, url, body, http.StatusCreated)
	if err != nil {
		fmt.Printf("failed to post comment: %v: %v\n", err, string(resp))
		return err
	}

	fmt.Printf("posted comment to pull/%v: %v\n", prNumber, string(resp))
	return nil
}

func (s *Server) pullRequestWebhook(c echo.Context) error {
	// Parse the relevant parts of the webhook.
	type Repository struct {
		FullName string `json:"full_name"`
	}

	type Installation struct {
		ID int64 `json:"id"`
	}

	type PRHook struct {
		Action       string       `json:"action"`
		Number       int          `json:"number"`
		Repository   Repository   `json:"repository"`
		Installation Installation `json:"installation"`
	}

	var prhook PRHook
	err := c.Bind(&prhook)
	if err != nil {
		// Print error body to logs, but not in response (no error oracles).
		fmt.Printf("error unmarshaling request: %v\n", err)
		return c.String(
			http.StatusInternalServerError, fmt.Sprintf("ignoring request: unmarshaling error"),
		)
	}

	// We are only interested in the determined-ai/determined repository.
	if prhook.Repository.FullName != "determined-ai/determined" {
		return c.String(http.StatusBadRequest, "ignoring request; repo != determined-ai/determined")
	}

	// We are only interested in new pull requests.
	if prhook.Action != "opened" {
		return c.String(http.StatusBadRequest, "ignoring request; action != opened")
	}

	err = s.postShortcutComment(prhook.Installation.ID, prhook.Number)
	if err != nil {
		return c.String(
			http.StatusInternalServerError, fmt.Sprintf("failed to post comment"),
		)
	}

	return c.String(
		http.StatusOK, fmt.Sprintf("posted comment to pull/%v\n", prhook.Number),
	)
}

func (s *Server) issueCommentWebhook(c echo.Context) error {
	// Parse the relevant parts of the webhook.
	type Issue struct {
		Number int `json:"number"`
	}

	type User struct {
		Login string `json:"login"`
	}

	type Comment struct {
		User User `json:"user"`
		Body string `json:"body"`
	}

	type Repository struct {
		FullName string `json:"full_name"`
	}

	type Installation struct {
		ID int64 `json:"id"`
	}

	type PRHook struct {
		Action       string       `json:"action"`
		Issue        Issue        `json:"issue"`
		Comment      Comment      `json:"comment"`
		Repository   Repository   `json:"repository"`
		Installation Installation `json:"installation"`
	}

	var prhook PRHook
	err := c.Bind(&prhook)
	if err != nil {
		// Print error body to logs, but not in response (no error oracles).
		fmt.Printf("error unmarshaling request: %v\n", err)
		return c.String(
			http.StatusInternalServerError, fmt.Sprintf("ignoring request: unmarshaling error"),
		)
	}

	// We are only interested in the determined-ai/determined repository.
	if prhook.Repository.FullName != "determined-ai/determined" {
		return c.String(http.StatusBadRequest, "ignoring request; repo != determined-ai/determined")
	}

	// We are only interested in created or edited comments.
	if prhook.Action != "created" && prhook.Action != "edited" {
		return c.String(http.StatusBadRequest, "ignoring request; action not in [created, edited]")
	}

	// Don't ever respond to our own comments.
	if prhook.Comment.User.Login == "det-ci[bot]" {
		return c.String(http.StatusBadRequest, "ignoring request; not responding to own comment")
	}

	// Only respond to the command: "@det-ci shortcuts".
	if !strings.Contains(prhook.Comment.Body, "@det-ci[bot] shortcuts") {
		return c.String(http.StatusBadRequest, "ignoring request; no command found")
	}

	err = s.postShortcutComment(prhook.Installation.ID, prhook.Issue.Number)
	if err != nil {
		return c.String(
			http.StatusInternalServerError, fmt.Sprintf("failed to post comment"),
		)
	}

	return c.String(
		http.StatusOK, fmt.Sprintf("posted comment to pull/%v\n", prhook.Issue.Number),
	)
}

// POST /app-hook
func (s *Server) githubAppHook(c echo.Context) error {
	// Do some initial routing by X-Github-Event header.
	hdr, ok := c.Request().Header["X-Github-Event"]
	if !ok {
		return c.String(http.StatusBadRequest, "ignoring request; X-Github-Event header missing")
	}
	switch hdr[0] {
	case "pull_request":
		return s.pullRequestWebhook(c)
	case "issue_comment":
		return s.issueCommentWebhook(c)
	}
	return c.String(
		http.StatusBadRequest, fmt.Sprintf("ignoring request; X-Github-Event = %v", hdr[0]),
	)
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

// Make the github app behavior optional to make local development eaiser (local github apps
// isn't really feasible anyway, since a local server won't recieve the github webhooks).
func configureGithubApp() (github.Github, bool, error) {
	var gh github.Github
	appidStr, ok := os.LookupEnv("GITHUB_APP_ID")
	if !ok {
		println("warning: no GITHUB_APP_ID found, will not run github app")
		return gh, false, nil
	}

	appid, err := strconv.ParseInt(appidStr, 10, 64)
	if err != nil {
		return gh, false, fmt.Errorf("failed to parse GITHUB_APP_ID: %s", err)
	}

	keypath, ok := os.LookupEnv("GITHUB_APP_KEYPATH")
	if !ok {
		println("warning: no GITHUB_APP_KEYPATH found, will not run github app")
		return gh, false, nil
	}

	gh, err = github.New(appid, keypath)
	if err != nil {
		return gh, false, err
	}

	return gh, true, nil
}

func main() {
	if len(os.Args) < 3 {
		fmt.Printf("usage: shortcuts SQLITE_PATH SERVER_SPEC\n")
		fmt.Printf("example: shortcuts sqlite.db :5729\n")
		fmt.Printf("environment variables:\nGITHUB_APP_ID: the, \n")
		fmt.Printf("    GITHUB_APP_ID: the App ID from github.com/settings/apps/APP-NAME\n")
		fmt.Printf("    GITHUB_APP_KEYPATH: ")
		fmt.Printf("path to private key generated from github.com/settings/apps/APP-NAME\n")
		os.Exit(1)
	}

	gh, runGithubApp, err := configureGithubApp()
	if err != nil {
		log.Fatal(err)
	}

	dbPath := os.Args[1]
	srvSpec := os.Args[2]

	client := Client{&http.Client{}}
	_, err = client.StartupDB(dbPath)
	if err != nil {
		log.Fatal(err)
	}

	srv := Server{client, gh}

	ready := make(chan struct{})

	go RefreshArtifactsThread(&client, ready)
	go RefreshBuildsThread(&client, ready)

	e := echo.New()
	e.GET("/pull/:pull", srv.getPull)
	e.GET("/pull/:pull/agent-coverage", srv.getAgent)
	e.GET("/pull/:pull/harness-coverage", srv.getHarness)
	e.GET("/pull/:pull/master-coverage", srv.getMaster)
	e.GET("/pull/:pull/model-hub-coverage", srv.getModelHub)
	e.GET("/pull/:pull/webui-coverage", srv.getWebui)
	if runGithubApp {
		e.POST("/app-hook", srv.githubAppHook)
	}
	e.Logger.Fatal(e.Start(srvSpec))
}
