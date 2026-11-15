// Package client sends form-encoded POST requests to the Next Engine API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Version is stamped into the User-Agent.
var Version = "dev"

// APIError is a response whose result is "error" or "redirect".
type APIError struct {
	Result  string // "error" or "redirect"
	Code    string
	Message any // string, or a per-record array for bulk updates
	Body    map[string]any
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Next Engine API %s %s: %v", e.Result, e.Code, e.Message)
}

// HTTPError is a non-2xx response or a body that is not JSON.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("unexpected HTTP %d from Next Engine: %s", e.Status, e.Body)
}

type Client struct {
	HTTP *http.Client
}

func New(timeout time.Duration) *Client {
	return &Client{HTTP: &http.Client{Timeout: timeout}}
}

// Post sends params to host+path and decodes the JSON object in the reply.
// A reply with result=error/redirect is returned as *APIError, with the
// decoded body still available so rotated tokens are not lost.
func (c *Client) Post(ctx context.Context, host, path string, params map[string]string) (map[string]any, error) {
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(host, "/")+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ne-cli/"+Version)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return nil, &HTTPError{Status: resp.StatusCode, Body: snippet(raw)}
	}
	switch result, _ := body["result"].(string); result {
	case "error", "redirect":
		code, _ := body["code"].(string)
		return body, &APIError{Result: result, Code: code, Message: body["message"], Body: body}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return body, &HTTPError{Status: resp.StatusCode, Body: snippet(raw)}
	}
	return body, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
