// Package jira implements the Connector interface against Jira Cloud using the
// REST API v3. Jira v3 stores rich text fields as Atlassian Document Format; the
// connector converts those fields to and from plain text so ARchetipo can keep
// persisting markdown bodies deterministically. All HTTP goes through Doer so
// tests can inject a fake transport.
package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// Doer abstracts http.Client so tests can replay canned responses without a
// network. The real implementation is a plain *http.Client.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// NewRealDoer returns a Doer backed by an http.Client with a sane timeout.
func NewRealDoer() Doer { return &http.Client{Timeout: 30 * time.Second} }

// do performs an authenticated REST call. method/path are the HTTP verb and the
// path relative to BaseURL (e.g. "/rest/api/3/issue"). body, when non-nil, is
// JSON-encoded as the request payload. out, when non-nil, receives the decoded
// JSON response. A non-2xx status is mapped to a typed connector error.
func (c *Connector) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return iox.NewInternal("encoding jira request body", err)
		}
		reader = bytes.NewReader(raw)
	}
	url := strings.TrimRight(c.jira.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return iox.NewInternal("building jira request", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Basic "+basicAuth(c.email, c.token))

	resp, err := c.doer.Do(req)
	if err != nil {
		return iox.NewConnector(iox.CodeConnectorNetwork,
			"jira request failed: "+err.Error(),
			"check jira.base_url and your network connection", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classify(resp.StatusCode, payload)
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return iox.NewConnector(iox.CodeConnectorBackend,
			"decoding jira response", "the Jira REST API returned unexpected JSON", err)
	}
	return nil
}

func basicAuth(email, token string) string {
	return base64.StdEncoding.EncodeToString([]byte(email + ":" + token))
}

// classify maps an HTTP status + Jira error payload to a stable connector error
// code. Jira encodes errors as {"errorMessages":[...],"errors":{...}}.
func classify(status int, payload []byte) error {
	msg := jiraErrorMessage(payload)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return iox.NewConnector(iox.CodeConnectorAuth,
			fmt.Sprintf("jira authentication failed (HTTP %d): %s", status, msg),
			"check JIRA_EMAIL/JIRA_API_TOKEN and that the token has access to the project", nil)
	case http.StatusNotFound:
		return iox.NewConnector(iox.CodeNotFound,
			fmt.Sprintf("jira resource not found: %s", msg), "", nil)
	case http.StatusConflict:
		return iox.NewConnector(iox.CodeConflict, msg, "", nil)
	case http.StatusBadRequest:
		return iox.NewConnector(iox.CodeConnectorBackend,
			"jira rejected the request: "+msg,
			"check the project key, issue types and custom field ids in .archetipo/config.yaml", nil)
	default:
		return iox.NewConnector(iox.CodeConnectorBackend,
			fmt.Sprintf("jira request failed (HTTP %d): %s", status, msg), "", nil)
	}
}

func jiraErrorMessage(payload []byte) string {
	var e struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(payload, &e); err != nil {
		if s := strings.TrimSpace(string(payload)); s != "" {
			return s
		}
		return "unknown error"
	}
	parts := append([]string(nil), e.ErrorMessages...)
	for k, v := range e.Errors {
		parts = append(parts, k+": "+v)
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, "; ")
}
