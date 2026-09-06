// Package vaultstore resolves pinned KV v2 references only in the core process.
package vaultstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	bao "github.com/openbao/openbao/api/v2"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const Prefix = "vault-kv2:"

var pathPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*(/[A-Za-z0-9][A-Za-z0-9_.-]*)*$`)
var orgPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Reference struct {
	Path    string `json:"path"`
	Key     string `json:"key"`
	Version int    `json:"version"`
	Store   string `json:"store,omitempty"`
}
type Config struct {
	Address, Mount, Namespace, Token, CAFile string
	HTTPClient                               *http.Client
}
type Store struct {
	client    *bao.Client
	mount, id string
}

func Parse(raw string) (*Reference, error) {
	if !strings.HasPrefix(raw, Prefix) {
		return nil, nil
	}
	if len(raw) > 1024 {
		return nil, errors.New("invalid external credential reference")
	}
	var r Reference
	d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(raw, Prefix)))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || !pathPattern.MatchString(r.Path) || len(r.Path) > 200 || !pathPattern.MatchString(r.Key) || strings.Contains(r.Key, "/") || len(r.Key) > 80 || r.Version < 1 || r.Version > 2147483647 || (r.Store != "" && !orgPattern.MatchString(r.Store)) {
		return nil, errors.New("invalid external credential reference")
	}
	return &r, nil
}
func Encode(r Reference) string          { b, _ := json.Marshal(r); return Prefix + string(b) }
func (s *Store) Bind(r Reference) string { r.Store = s.id; return Encode(r) }
func New(c Config) (*Store, error) {
	u, err := url.Parse(c.Address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !pathPattern.MatchString(c.Mount) || len(c.Mount) > 100 || (c.Namespace != "" && !pathPattern.MatchString(c.Namespace)) || len(c.Namespace) > 200 || len(c.Token) < 8 || len(c.Token) > 16384 {
		return nil, errors.New("invalid external secret store configuration")
	}
	cfg := bao.NewConfig()
	cfg.Address = strings.TrimRight(c.Address, "/")
	cfg.MaxRetries = 0
	cfg.DisableRedirects = true
	cfg.Timeout = 15 * time.Second
	transport := cfg.HttpClient.Transport.(*http.Transport)
	transport.Proxy = nil
	if c.CAFile != "" {
		if err = cfg.ConfigureTLS(&bao.TLSConfig{CACert: c.CAFile}); err != nil {
			return nil, errors.New("invalid secret store CA configuration")
		}
	}
	if c.HTTPClient != nil {
		copy := *c.HTTPClient
		cfg.HttpClient = &copy
	}
	cfg.HttpClient.Timeout = 15 * time.Second
	cfg.HttpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base := cfg.HttpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	cfg.HttpClient.Transport = boundedTransport{base}
	client, err := bao.NewClient(cfg)
	if err != nil {
		return nil, errors.New("invalid external secret store client")
	}
	client.SetToken(c.Token)
	client.SetNamespace(c.Namespace)
	digest := sha256.Sum256([]byte(cfg.Address + "\n" + c.Namespace + "\n" + c.Mount + "\nprovidah-v1"))
	return &Store{client: client, mount: c.Mount, id: hex.EncodeToString(digest[:])}, nil
}

type boundedTransport struct{ http.RoundTripper }

func (t boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.RoundTripper.RoundTrip(r)
	if err == nil && response.Body != nil {
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(response.Body, 64<<10), response.Body}
	}
	return response, err
}
func (s *Store) Resolve(ctx context.Context, org, cloud string, r Reference) (string, error) {
	unavailable := errors.New("external credential unavailable")
	if s == nil || r.Store != s.id || !orgPattern.MatchString(org) || (cloud != "aws" && cloud != "digitalocean" && cloud != "hetzner") {
		return "", unavailable
	}
	if _, err := Parse(Encode(r)); err != nil {
		return "", unavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	secret, err := s.client.KVv2(s.mount).GetVersion(ctx, "providah/"+org+"/"+cloud+"/"+r.Path, r.Version)
	if err != nil || secret == nil || secret.VersionMetadata == nil || secret.VersionMetadata.Version != r.Version || secret.VersionMetadata.Destroyed || !secret.VersionMetadata.DeletionTime.IsZero() {
		return "", unavailable
	}
	value, ok := secret.Data[r.Key].(string)
	if !ok || len(value) < 8 || len(value) > 16384 || strings.HasPrefix(value, Prefix) {
		return "", unavailable
	}
	return value, nil
}
