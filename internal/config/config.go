package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that is written as a Go duration string in YAML.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

type Config struct {
	Database  string `yaml:"database"`
	AudioDir  string `yaml:"audio_dir"`
	OutputDir string `yaml:"output_dir"`
	LogLevel  string `yaml:"log_level"`

	TTS   TTS   `yaml:"tts"`
	Merge Merge `yaml:"merge"`
}

type TTS struct {
	URL          string            `yaml:"url"`
	Character    string            `yaml:"character"`
	Timeout      Duration          `yaml:"timeout"`
	Retries      int               `yaml:"retries"`
	RetryBackoff Duration          `yaml:"retry_backoff"`
	Headers      map[string]string `yaml:"headers"`
}

type Merge struct {
	Overwrite          bool     `yaml:"overwrite"`
	GapBetweenSegments Duration `yaml:"gap_between_segments"`
	LeadingTrailingGap bool     `yaml:"leading_trailing_gap"`
}

func Default() *Config {
	return &Config{
		Database:  "./data/tts.db",
		AudioDir:  "./data/audio",
		OutputDir: "./data/output",
		LogLevel:  "info",
		TTS: TTS{
			URL:          "http://127.0.0.1:8080/tts",
			Character:    "default",
			Timeout:      Duration(60 * time.Second),
			Retries:      2,
			RetryBackoff: Duration(2 * time.Second),
			Headers:      map[string]string{},
		},
		Merge: Merge{
			Overwrite:          true,
			GapBetweenSegments: Duration(200 * time.Millisecond),
		},
	}
}

// Load reads path over the built-in defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := Default()
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Database) == "" {
		return fmt.Errorf("database must not be empty")
	}
	if strings.TrimSpace(c.AudioDir) == "" {
		return fmt.Errorf("audio_dir must not be empty")
	}
	if strings.TrimSpace(c.OutputDir) == "" {
		return fmt.Errorf("output_dir must not be empty")
	}

	u, err := url.Parse(c.TTS.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("tts.url must be a valid http(s) URL")
	}
	if c.TTS.Timeout.Std() <= 0 {
		return fmt.Errorf("tts.timeout must be greater than zero")
	}
	if c.TTS.Retries < 0 {
		return fmt.Errorf("tts.retries must not be negative")
	}
	if c.TTS.RetryBackoff.Std() < 0 {
		return fmt.Errorf("tts.retry_backoff must not be negative")
	}
	for k := range c.TTS.Headers {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("tts.headers contains an empty header name")
		}
	}
	if c.Merge.GapBetweenSegments.Std() < 0 {
		return fmt.Errorf("merge.gap_between_segments must not be negative")
	}

	switch strings.ToLower(strings.TrimSpace(c.LogLevel)) {
	case "", "info":
		c.LogLevel = "info"
	case "debug":
		c.LogLevel = "debug"
	case "warn", "warning":
		c.LogLevel = "warn"
	case "error":
		c.LogLevel = "error"
	default:
		return fmt.Errorf("invalid log_level %q (want debug, info, warn or error)", c.LogLevel)
	}

	if c.TTS.Headers == nil {
		c.TTS.Headers = map[string]string{}
	}
	return nil
}

// VoiceKey returns the first 16 hex characters of SHA-256(character).
func VoiceKey(character string) string {
	sum := sha256.Sum256([]byte(character))
	return hex.EncodeToString(sum[:])[:16]
}
