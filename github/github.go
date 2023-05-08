package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"

	"github.com/bradleyfalzon/ghinstallation/v2"
)

type Github struct {
	// we use an intermediate AppsTransport to avoid errors on the .Client method.
	atr *ghinstallation.AppsTransport
	// We use a per-installation-id http.Client to interact with the github api
	// because the way a github app authenticates is with signed requests, which
	// are signed with the installation id included.
	clients map[int64]*http.Client
	mutex sync.Mutex
}

func New(appID int64, keypath string) (Github, error) {
	tr := http.DefaultTransport
	atr, err := ghinstallation.NewAppsTransportKeyFromFile(tr, appID, keypath)
	if err != nil {
		return Github{}, err
	}

	return Github{
		atr:     atr,
		clients: map[int64]*http.Client{},
	}, nil
}

func (g *Github) Client(installationID int64) *http.Client {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	client, ok := g.clients[installationID]
	if ok {
		return client
	}
	tr := ghinstallation.NewFromAppsTransport(g.atr, installationID)
	client = &http.Client{Transport: tr}
	g.clients[installationID] = client
	return client
}

func (g *Github) Post(
	installationID int64, url string, body map[string]interface{}, expect int,
) ([]byte, error) {
	byts, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(byts))
	if err != nil {
		return nil, err
	}

	req.Header.Add("Accept", "application/json")

	resp, err := g.Client(installationID).Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	out, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Check for non-200 response
	if resp.StatusCode != expect {
		return nil, fmt.Errorf("unexpected response code (%v): %v", resp.Status, string(out))
	}

	return out, nil
}
