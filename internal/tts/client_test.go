package tts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LiZeC123/kokomi-narrator/internal/config"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func TestSynthesizeURIEncodesText(t *testing.T) {
	const rawText = "你好 世界+A/B?"

	var (
		gotText string
		rawBody []byte
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
			Character string `json:"character"`
			Text      string `json:"text"`
		}
		rawBody, _ = io.ReadAll(r.Body)
		if err := json.Unmarshal(rawBody, &req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Character != "kokomi" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		gotText = req.Text
		_, _ = w.Write(wavutil.Encode(format, make([]byte, 32)))
	}))
	defer srv.Close()

	client := New(config.TTS{
		URL:       srv.URL,
		Character: "kokomi",
		Timeout:   config.Duration(5 * time.Second),
	})
	wav, err := client.Synthesize(context.Background(), rawText)
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if len(wav) == 0 {
		t.Fatal("Synthesize() returned empty audio")
	}

	want := strings.ReplaceAll(url.QueryEscape(rawText), "+", "%20")
	if gotText != want {
		t.Fatalf("encoded text = %q, want %q", gotText, want)
	}
	if strings.Contains(string(rawBody), "你好") {
		t.Fatalf("request body contains raw text: %s", rawBody)
	}
	decoded, err := url.QueryUnescape(gotText)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != rawText {
		t.Fatalf("decoded text = %q, want %q", decoded, rawText)
	}
}
