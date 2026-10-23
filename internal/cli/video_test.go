package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func TestVideoCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake audio2video script requires a POSIX shell")
	}

	enterTestDir(t)
	writeTestConfig(t, "http://127.0.0.1:1/tts")

	if err := os.WriteFile("a.txt", []byte("第一段\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, "add", "a.txt")
	if code != 0 {
		t.Fatalf("add exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	format := testWAVFormat()
	if err := os.MkdirAll(filepath.Join("data", "output"), 0o755); err != nil {
		t.Fatal(err)
	}
	audioPath := filepath.Join("data", "output", "a.wav")
	if err := os.WriteFile(audioPath, wavutil.Encode(format, make([]byte, format.ByteRate)), 0o644); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join("data", "image.jpg")
	if err := os.WriteFile(imagePath, []byte("fake image"), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	argsLog := filepath.Join(t.TempDir(), "args.txt")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_AUDIO2VIDEO_ARGS"
out=""
while [ "$#" -gt 0 ]; do
	if [ "$1" = "-o" ]; then
		out="$2"
		shift
	fi
	shift
done
printf 'fake mp4\n' > "$out"
`
	scriptPath := filepath.Join(binDir, "audio2video")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_AUDIO2VIDEO_ARGS", argsLog)

	code, stdout, stderr = runCLI(t, "video", "1")
	if code != 0 {
		t.Fatalf("video exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	videoPath := filepath.Join("data", "output", "a.mp4")
	data, err := os.ReadFile(videoPath)
	if err != nil {
		t.Fatalf("read video: %v", err)
	}
	if string(data) != "fake mp4\n" {
		t.Fatalf("video content = %q", data)
	}
	args, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	argsText := string(args)
	for _, want := range []string{
		"-a\n" + audioPath + "\n",
		"-i\n" + imagePath + "\n",
		"-o\n" + filepath.Join("data", "output", "a.tmp.mp4") + "\n",
	} {
		if !strings.Contains(argsText, want) {
			t.Fatalf("audio2video args = %q, missing %q", argsText, want)
		}
	}

	// --image overrides video.image, and the command also accepts a filename.
	otherImage := filepath.Join("data", "other.png")
	if err := os.WriteFile(otherImage, []byte("other image"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runCLI(t, "video", "a.txt", "--image", otherImage)
	if code != 0 {
		t.Fatalf("video --image exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	args, err = os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "-i\n"+otherImage+"\n") {
		t.Fatalf("audio2video args after --image = %q", args)
	}

	// Missing final audio is a precondition failure.
	if err := os.Remove(audioPath); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runCLI(t, "video", "1")
	if code != 2 {
		t.Fatalf("video without audio exit = %d, want 2", code)
	}

	// Missing cover image is a precondition failure.
	if err := os.WriteFile(audioPath, wavutil.Encode(format, make([]byte, format.ByteRate)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(imagePath); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runCLI(t, "video", "1")
	if code != 2 {
		t.Fatalf("video without image exit = %d, want 2", code)
	}

	// Missing audio2video is an environment error.
	if err := os.WriteFile(imagePath, []byte("fake image"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	code, _, _ = runCLI(t, "video", "1")
	if code != 1 {
		t.Fatalf("video without audio2video exit = %d, want 1", code)
	}
}
