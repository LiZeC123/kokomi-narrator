package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LiZeC123/kokomi-narrator/internal/config"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

const maxResponseBytes = 64 << 20

type Client struct {
	cfg  config.TTS
	http *http.Client
}

func New(cfg config.TTS) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{},
	}
}

type requestBody struct {
	Character    string `json:"character"`
	Text         string `json:"text"`
	TextLanguage string `json:"text_language"`
}

// Synthesize requests one segment of speech and returns a validated PCM WAV.
func (c *Client) Synthesize(ctx context.Context, text string) ([]byte, error) {
	payload, err := json.Marshal(requestBody{
		Character: c.cfg.Character,
		Text:      encodeText(text),
		TextLanguage: "all_zh",
	})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	attempts := c.cfg.Retries + 1
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			if err := sleepContext(ctx, c.cfg.RetryBackoff.Std()); err != nil {
				return nil, err
			}
		}

		data, err, retryable := c.once(ctx, payload)
		if err == nil {
			return data, nil
		}
		lastErr = err

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !retryable || i == attempts-1 {
			break
		}
		slog.Debug("tts request failed, retrying",
			"attempt", i+1,
			"attempts", attempts,
			"error", err,
		)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("tts request failed")
	}
	return nil, lastErr
}

func (c *Client) once(ctx context.Context, payload []byte) ([]byte, error, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout.Std())
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err), false
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err), true
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, summarizeBody(body))
		retryable := resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests
		return nil, fmt.Errorf("%s", msg), retryable
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err), true
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes), true
	}
	if _, err := wavutil.Parse(body); err != nil {
		return nil, fmt.Errorf("invalid WAV response: %w", err), true
	}
	return body, nil, false
}

// encodeText percent-encodes text for the TTS service. Spaces are emitted as
// %20 (not "+") so both unquote and unquote_plus servers decode them the same.
func encodeText(text string) string {
	return strings.ReplaceAll(url.QueryEscape(text), "+", "%20")
}

func summarizeBody(body []byte) string {
	s := strings.ToValidUTF8(string(body), "?")
	s = strings.Join(strings.Fields(s), " ")
	return TruncateRunes(s, 200)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// TruncateRunes shortens s to at most n runes and appends "..." when needed.
func TruncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "..."
}
