package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func TestMakeHonorsConfiguredConcurrency(t *testing.T) {
	tests := []struct {
		name        string
		concurrency int
		wantMax     int
	}{
		{name: "two", concurrency: 2, wantMax: 2},
		{name: "one", concurrency: 1, wantMax: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			oldWd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(oldWd) })

			var (
				mu        sync.Mutex
				active    int
				maxActive int
				requests  int
			)
			format := wavutil.Format{
				AudioFormat:   1,
				Channels:      1,
				SampleRate:    8000,
				ByteRate:      16000,
				BlockAlign:    2,
				BitsPerSample: 16,
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Text string `json:"text"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}

				mu.Lock()
				active++
				requests++
				if active > maxActive {
					maxActive = active
				}
				mu.Unlock()

				time.Sleep(150 * time.Millisecond)

				mu.Lock()
				active--
				mu.Unlock()
				_, _ = w.Write(wavutil.Encode(format, make([]byte, 64)))
			}))
			defer srv.Close()

			cfg := fmt.Sprintf(`database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
log_level: error
tts:
  url: "%s"
  character: default
  concurrency: %d
  timeout: 5s
  retries: 0
  retry_backoff: 10ms
`, srv.URL, tt.concurrency)
			if err := os.WriteFile("config.yaml", []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("a.txt", []byte("一\n二\n三\n四\n五\n六\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			code, stdout, stderr := runCLI(t, "add", "a.txt")
			if code != 0 {
				t.Fatalf("add exit code = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			code, stdout, stderr = runCLI(t, "make", "1")
			if code != 0 {
				t.Fatalf("make exit code = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}

			mu.Lock()
			gotMax, gotRequests := maxActive, requests
			mu.Unlock()
			if gotMax != tt.wantMax {
				t.Fatalf("max concurrent requests = %d, want %d", gotMax, tt.wantMax)
			}
			if gotRequests != 6 {
				t.Fatalf("requests = %d, want 6", gotRequests)
			}
		})
	}
}
