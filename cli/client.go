package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// apiTimeout bounds one plain API call (send, answer, decode): a healthy API answers in well under a second, but a
// complete can check a large file first (a LoRA's hash). Event streams and file transfers are not cut by it: they
// run under their caller's ctx (helm mcp: the tool call, which the client's cancel ends).
var apiTimeout = 2 * time.Minute

// config is ~/.config/helm/config.json (0600). The API key itself is never stored: it comes from HELM_API_KEY or
// the file at APIKeyFile.
type config struct {
	URL          string `json:"url"`
	APIKeyFile   string `json:"api_key_file,omitempty"`
	Email        string `json:"email,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "helm", "config.json"), nil
}

func loadConfig() (config, error) {
	var c config
	p, err := configPath()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, errors.New("not logged in: run helm login")
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func saveConfig(c config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(p, 0o600) // WriteFile keeps the mode of an existing file
}

// apiKey is HELM_API_KEY, else the contents of the configured key file.
func apiKey(c config) (string, error) {
	if k := os.Getenv("HELM_API_KEY"); k != "" {
		return k, nil
	}
	if c.APIKeyFile == "" {
		return "", errors.New("no API key: set HELM_API_KEY or pass --api-key-file to helm login")
	}
	b, err := os.ReadFile(c.APIKeyFile)
	if err != nil {
		return "", fmt.Errorf("read API key: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// client calls the API with the client's key and the user's token; a 401 refreshes the tokens once and retries.
// helm mcp calls it from several goroutines: mu guards the tokens, and one refresh runs at a time (rmu).
type client struct {
	cfg  config
	key  string
	app  string // X-Helm-Client (API 1.49: a timeline revision's via): helm, or mcp in helm mcp
	http *http.Client
	mu   sync.Mutex
	rmu  sync.Mutex
}

func (c *client) token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.AccessToken
}

func newClient(c config) (*client, error) {
	key, err := apiKey(c)
	if err != nil {
		return nil, err
	}
	return &client{cfg: c, key: key, app: "helm", http: &http.Client{}}, nil
}

// apiError is a non-2xx answer; detail is the server's {"detail"} when it sent one.
type apiError struct {
	status int
	detail string
}

func (e *apiError) Error() string { return fmt.Sprintf("%d: %s", e.status, e.detail) }

func readError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var body struct {
		Detail  string `json:"detail"`
		Message string `json:"message"`
	}
	d := strings.TrimSpace(string(b))
	if json.Unmarshal(b, &body) == nil && body.Detail+body.Message != "" {
		d = body.Detail + body.Message
	}
	return &apiError{status: resp.StatusCode, detail: d}
}

func (c *client) send(ctx context.Context, method, path string, body []byte, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	req.Header.Set("X-API-Key", c.key)
	if c.app != "" {
		req.Header.Set("X-Helm-Client", c.app)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := c.token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return c.http.Do(req)
}

// do sends the request; on a 401 it refreshes the tokens (saving them) and sends it once more.
func (c *client) do(ctx context.Context, method, path string, in any, hdr http.Header) (*http.Response, error) {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	stale, canRefresh := c.cfg.AccessToken, c.cfg.RefreshToken != ""
	c.mu.Unlock()
	resp, err := c.send(ctx, method, path, body, hdr)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || !canRefresh {
		return resp, err
	}
	resp.Body.Close()
	if err := c.refresh(ctx, stale); err != nil {
		return nil, err
	}
	return c.send(ctx, method, path, body, hdr)
}

// refresh gets new tokens unless another call already replaced the stale one.
func (c *client) refresh(ctx context.Context, stale string) error {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if c.token() != stale {
		return nil
	}
	var s session
	resp, err := c.send(ctx, http.MethodPost, "/auth/refresh", mustJSON(map[string]string{"refresh_token": c.cfg.RefreshToken}), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("session expired, run helm login (%w)", readError(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return err
	}
	c.mu.Lock()
	c.cfg.AccessToken, c.cfg.RefreshToken = s.AccessToken, s.RefreshToken
	cfg := c.cfg
	c.mu.Unlock()
	return saveConfig(cfg)
}

// call sends a JSON request and decodes a JSON answer into out (nil = discard); a status other than want is an error.
func (c *client) call(method, path string, in any, want int, out any) error {
	return c.callCtx(context.Background(), method, path, in, want, out)
}

// callCtx is call under ctx, and at most apiTimeout.
func (c *client) callCtx(ctx context.Context, method, path string, in any, want int, out any) error {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	resp, err := c.do(ctx, method, path, in, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return readError(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type user struct {
	ID    string   `json:"id"`
	Email string   `json:"email"`
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

type session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         user   `json:"user"`
}
