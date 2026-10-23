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

func TestBGMAndMixDefaultsAndOverride(t *testing.T) {
	if got := Default().BGM.Dir; got != "./data/bgm" {
		t.Fatalf("Default().BGM.Dir = %q, want ./data/bgm", got)
	}
	if got := Default().Mix.BGMVolume; got != 0.10 {
		t.Fatalf("Default().Mix.BGMVolume = %v, want 0.10", got)
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
	if cfg.BGM.Dir != "./data/bgm" || cfg.Mix.BGMVolume != 0.10 {
		t.Fatalf("defaults = %q/%.2f, want ./data/bgm/0.10", cfg.BGM.Dir, cfg.Mix.BGMVolume)
	}

	path = writeConfig(t, `database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
tts:
  url: http://127.0.0.1:8080/tts
  character: default
bgm:
  dir: /music/bgm
mix:
  bgm_volume: 0.15
`)
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.BGM.Dir != "/music/bgm" || cfg.Mix.BGMVolume != 0.15 {
		t.Fatalf("override = %q/%.2f, want /music/bgm/0.15", cfg.BGM.Dir, cfg.Mix.BGMVolume)
	}
}

func TestBGMAndMixValidation(t *testing.T) {
	base := `database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
tts:
  url: http://127.0.0.1:8080/tts
  character: default
`
	path := writeConfig(t, base+`bgm:
  dir: ""
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an empty bgm.dir")
	}

	path = writeConfig(t, base+`mix:
  bgm_volume: 1.5
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted bgm_volume 1.5")
	}

	path = writeConfig(t, base+`mix:
  bgm_volume: -0.1
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted a negative bgm_volume")
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
