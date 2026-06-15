package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner runs a lens agent: it takes the argv, the brief on stdin, and a timeout,
// and returns the agent's stdout. A non-nil error degrades the lens to "no LLM
// output" rather than aborting the run.
type Runner func(ctx context.Context, dir string, args []string, input []byte, timeout time.Duration) (string, error)

// DefaultRunner returns the production Runner for an agent: a loopback-only HTTP
// runner for ollama, and a process-exec runner for everything else.
func DefaultRunner(agent string) Runner {
	if agent == "ollama" {
		return execOllama
	}
	return execProcess
}

func execProcess(ctx context.Context, dir string, args []string, input []byte, timeout time.Duration) (string, error) {
	if len(args) == 0 {
		return "", errors.New("lens: empty agent command")
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, args[0], args[1:]...)
	command.Dir = dir
	command.Stdin = bytes.NewReader(input)
	stdout := newCappedBuffer(MaxOutputBytes)
	stderr := newCappedBuffer(MaxOutputBytes)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("agent timed out after %s", timeout)
	}
	if err != nil {
		warning := strings.TrimSpace(stderr.String())
		if warning == "" {
			warning = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("agent failed: %w: %s", err, truncateWarning(warning))
	}
	if stdout.Exceeded() {
		return "", fmt.Errorf("agent output exceeds %d bytes", MaxOutputBytes)
	}
	return stdout.String(), nil
}

type cappedBuffer struct {
	limit    int
	exceeded bool
	buf      bytes.Buffer
}

func newCappedBuffer(limit int) cappedBuffer { return cappedBuffer{limit: limit} }

func (w *cappedBuffer) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		w.exceeded = true
		return len(p), nil
	}
	remaining := w.limit - w.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = w.buf.Write(p)
			return len(p), nil
		}
		_, _ = w.buf.Write(p[:remaining])
	}
	w.exceeded = true
	return len(p), nil
}

func (w *cappedBuffer) String() string { return w.buf.String() }
func (w *cappedBuffer) Exceeded() bool { return w.exceeded }

func truncateWarning(warning string) string {
	const maxWarningBytes = 4000
	if len(warning) <= maxWarningBytes {
		return warning
	}
	return warning[:maxWarningBytes] + "\n[truncated]"
}

// execOllama posts the brief to a loopback-only ollama endpoint. The dial path is
// pinned to loopback addresses so the runner cannot egress even if the configured
// URL resolves off-host.
func execOllama(ctx context.Context, dir string, args []string, input []byte, timeout time.Duration) (string, error) {
	_ = dir
	if len(args) < 3 || args[0] != "ollama" {
		return "", errors.New("lens: invalid ollama runner arguments")
	}
	model := strings.TrimSpace(args[1])
	if model == "" {
		return "", errors.New("lens: --agent ollama requires --model")
	}
	endpoint := strings.TrimSpace(os.Getenv("ENTIRE_BRAIN_OLLAMA_URL"))
	if endpoint == "" {
		endpoint = "http://127.0.0.1:11434/api/generate"
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("lens: parse ollama url: %w", err)
	}
	if !isLoopbackHTTPURL(u) {
		return "", fmt.Errorf("lens: ollama url must be loopback-only: %s", endpoint)
	}
	body, err := json.Marshal(map[string]any{
		"model":  model,
		"system": args[2],
		"prompt": string(input),
		"stream": false,
	})
	if err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(runCtx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	var tr *http.Transport
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = dt.Clone()
	} else {
		tr = &http.Transport{}
	}
	tr.Proxy = nil
	tr.DialContext = loopbackOnlyDialContext
	tr.DialTLS = nil
	tr.DialTLSContext = nil
	client := &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !isLoopbackHTTPURL(req.URL) {
				return fmt.Errorf("ollama redirect must stay loopback-only: %s", req.URL.String())
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("ollama timed out after %s", timeout)
	}
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxOutputBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > MaxOutputBytes {
		return "", fmt.Errorf("ollama output exceeds %d bytes", MaxOutputBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama returned HTTP %d: %s", resp.StatusCode, truncateWarning(string(data)))
	}
	var parsed struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("parse ollama response: %w", err)
	}
	if parsed.Error != "" {
		return "", fmt.Errorf("ollama error: %s", parsed.Error)
	}
	return parsed.Response, nil
}

func isLoopbackHTTPURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.Trim(u.Hostname(), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loopbackOnlyDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("ollama dial target must include host and port: %s", address)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve ollama loopback target %s: %w", host, err)
	}
	var lastErr error
	for _, ip := range ips {
		if !ip.IsLoopback() {
			continue
		}
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, fmt.Errorf("ollama loopback dial failed for %s: %w", address, lastErr)
	}
	return nil, fmt.Errorf("ollama dial target is not loopback-only: %s", address)
}
