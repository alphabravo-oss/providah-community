package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/inputspec"
	"github.com/alphabravo-oss/providah-community/internal/sourcebundle"
	"io"
	"net"
	"net/http"
	"time"
)

type Request struct {
	Inputs          json.RawMessage `json:"inputs,omitempty"`
	RuntimePolicy   string          `json:"runtime_policy"`
	DependencyProxy bool            `json:"dependency_proxy,omitempty"`
	Runtime         string          `json:"runtime"`
	Image           string          `json:"image"`
	Entrypoint      string          `json:"entrypoint"`
	SHA256          string          `json:"sha256"`
	Archive         []byte          `json:"archive"`
}
type Result struct {
	Status string `json:"status"`
}

func (r Request) Validate() error {
	m, e := sourcebundle.Validate(r.Archive, r.Runtime, r.Entrypoint)
	if e != nil {
		return e
	}
	if m.SHA256 != r.SHA256 {
		return errors.New("source hash mismatch")
	}
	if r.Inputs != nil {
		_, e = inputspec.Values(m.Inputs, r.Inputs)
		return e
	}
	return nil
}
func Client(socket string) func(context.Context, Request) (Result, error) {
	client := &http.Client{Timeout: 135 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	return func(ctx context.Context, r Request) (Result, error) {
		var result Result
		b, e := json.Marshal(r)
		if e != nil {
			return result, e
		}
		req, e := http.NewRequestWithContext(ctx, "POST", "http://launcher/automation/validate", bytes.NewReader(b))
		if e != nil {
			return result, e
		}
		req.Header.Set("Content-Type", "application/json")
		response, e := client.Do(req)
		if e != nil {
			return result, e
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != 200 {
			return result, errors.New("validation worker unavailable")
		}
		d := json.NewDecoder(io.LimitReader(response.Body, 1024))
		d.DisallowUnknownFields()
		if d.Decode(&result) != nil || d.Decode(new(any)) != io.EOF || (result.Status != "succeeded" && result.Status != "failed") {
			return Result{}, errors.New("invalid validation result")
		}
		return result, nil
	}
}
