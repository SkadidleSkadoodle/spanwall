// Package client talks to the background app's HTTP API.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Base is the background app's address.
const Base = "http://127.0.0.1:47920"

var (
	fast = &http.Client{Timeout: 2 * time.Second}
	slow = &http.Client{Timeout: 2 * time.Minute} // rendering can take a few seconds
)

// ErrNotRunning means nothing answered on the port.
var ErrNotRunning = errors.New("the Spanwall background app is not running")

// Running reports whether the background app answers.
func Running() bool {
	resp, err := fast.Get(Base + "/api/status")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Get calls GET path and decodes the JSON answer into out.
func Get(path string, out any) error {
	resp, err := slow.Get(Base + path)
	return decode(resp, err, out)
}

// Post sends in as JSON and decodes the JSON answer into out.
func Post(path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resp, err := slow.Post(Base+path, "application/json", bytes.NewReader(b))
	return decode(resp, err, out)
}

func decode(resp *http.Response, err error, out any) error {
	if err != nil {
		return fmt.Errorf("%w (%v)", ErrNotRunning, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}
