package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestVideoDefaultsAndOverride(t *testing.T) {
	if got := Default().Video.Image; got != "./data/image.jpg" {
		t.Fatalf("Default().Video.Image = %q, want ./data/image.jpg", got)
	}

	base := `database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
tts:
  url: http://127.0.0.1:8080/tts
  character: default
`
	cfg, err := Load(writeConfig(t, base))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Video.Image != "./data/image.jpg" {
		t.Fatalf("default video.image = %q", cfg.Video.Image)
	}

	cfg, err = Load(writeConfig(t, base+`video:
  image: ~/Pictures/cover.png
`))
	if err != nil {
		t.Fatalf("Load() with video.image error = %v", err)
	}
	if cfg.Video.Image != "~/Pictures/cover.png" {
		t.Fatalf("video.image = %q, want ~/Pictures/cover.png", cfg.Video.Image)
	}
}

func TestVideoValidation(t *testing.T) {
	path := writeConfig(t, `database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
tts:
  url: http://127.0.0.1:8080/tts
  character: default
video:
  image: ""
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an empty video.image")
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

func TestCheckDefaultsAndOverride(t *testing.T) {
	defaults := Default().Check
	if defaults.SilenceDB != -45 || defaults.SilenceDuration.Std() != time.Second || defaults.Concurrency != 4 {
		t.Fatalf("check defaults = %+v, want -45/1s/4", defaults)
	}

	base := `database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
tts:
  url: http://127.0.0.1:8080/tts
  character: default
`
	cfg, err := Load(writeConfig(t, base))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Check.SilenceDB != -45 || cfg.Check.SilenceDuration.Std() != time.Second || cfg.Check.Concurrency != 4 {
		t.Fatalf("check defaults = %+v", cfg.Check)
	}

	cfg, err = Load(writeConfig(t, base+`check:
  silence_db: -50
  silence_duration: 1500ms
  concurrency: 2
`))
	if err != nil {
		t.Fatalf("Load() with check override error = %v", err)
	}
	if cfg.Check.SilenceDB != -50 || cfg.Check.SilenceDuration.Std() != 1500*time.Millisecond || cfg.Check.Concurrency != 2 {
		t.Fatalf("check override = %+v", cfg.Check)
	}
}

func TestCheckValidation(t *testing.T) {
	base := `database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
tts:
  url: http://127.0.0.1:8080/tts
  character: default
`
	for name, body := range map[string]string{
		"positive silence_db": `check:
  silence_db: 1
`,
		"zero silence_duration": `check:
  silence_duration: 0s
`,
		"zero concurrency": `check:
  concurrency: 0
`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, base+body)); err == nil {
				t.Fatalf("Load() accepted %s", name)
			}
		})
	}
}
