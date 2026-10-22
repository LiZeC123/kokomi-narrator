package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConcurrencyDefaultAndOverride(t *testing.T) {
	if got := Default().TTS.Concurrency; got != 2 {
		t.Fatalf("Default().TTS.Concurrency = %d, want 2", got)
	}

	path := writeConfig(t, `database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
tts:
  url: http://127.0.0.1:8080/tts
  character: default
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.TTS.Concurrency != 2 {
		t.Fatalf("concurrency = %d, want default 2", cfg.TTS.Concurrency)
	}

	path = writeConfig(t, `database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
tts:
  url: http://127.0.0.1:8080/tts
  character: default
  concurrency: 3
`)
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.TTS.Concurrency != 3 {
		t.Fatalf("concurrency = %d, want 3", cfg.TTS.Concurrency)
	}
}

func TestConcurrencyMustBePositive(t *testing.T) {
	path := writeConfig(t, `database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
tts:
  url: http://127.0.0.1:8080/tts
  character: default
  concurrency: 0
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted concurrency 0")
	}
}
