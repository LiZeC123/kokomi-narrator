package cli

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractHTMLText(t *testing.T) {
	raw := `<html><head><title>ignored title</title><style>.x{}</style></head>` +
		`<body><nav>menu</nav><h1>标题 &amp; 1</h1>` +
		`<p>Hello <b>world</b>.<br/>Next line.</p>` +
		`<script>bad()</script></body></html>`

	got, err := extractHTMLText(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("extractHTMLText() error = %v", err)
	}
	want := "标题 & 1\n\nHello world.\n\nNext line."
	if got != want {
		t.Fatalf("extractHTMLText() = %q, want %q", got, want)
	}
}

func TestOutputFilename(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		fallback string
		want     string
	}{
		{"first line", "第一章\n\n正文", "chapter1", "第一章.txt"},
		{"empty text", "", "chapter2", "chapter2.txt"},
		{"slash in title", "Part 1/2\n\nbody", "chapter3", "chapter3.txt"},
		{"colon in title", "Chapter 2: Test\n\nbody", "chapter4", "Chapter 2: Test.txt"},
		{"too long title", strings.Repeat("章", 100) + "\nbody", "chapter5", "chapter5.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			used := make(map[string]bool)
			if got := outputFilename(".", tt.text, tt.fallback, used); got != tt.want {
				t.Fatalf("outputFilename() = %q, want %q", got, tt.want)
			}
		})
	}

	used := make(map[string]bool)
	if got := outputFilename(".", "重复\n\nbody", "ch4", used); got != "重复.txt" {
		t.Fatalf("first outputFilename() = %q, want 重复.txt", got)
	}
	if got := outputFilename(".", "重复\n\nother", "ch5", used); got != "ch5.txt" {
		t.Fatalf("collision outputFilename() = %q, want ch5.txt", got)
	}
}

func TestCleanZipPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"OEBPS/Text/ch1.xhtml", "OEBPS/Text/ch1.xhtml", true},
		{"./OEBPS/Text/ch1.xhtml", "OEBPS/Text/ch1.xhtml", true},
		{`OEBPS\Text\ch1.xhtml`, "OEBPS/Text/ch1.xhtml", true},
		{"../evil.xhtml", "", false},
		{"/abs.xhtml", "", false},
		{"OEBPS/../../evil.xhtml", "", false},
	}
	for _, tt := range tests {
		got, ok := cleanZipPath(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("cleanZipPath(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

type epubEntry struct {
	name    string
	content string
}

func writeTestEpub(t *testing.T, filename string, entries []epubEntry) {
	t.Helper()
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractCommand(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	writeTestEpub(t, "book.epub", []epubEntry{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container/>`},
		{"OEBPS/Text/ch1.xhtml", `<html><body><h1>第一章</h1><p>正文一。</p><p>正文二。</p></body></html>`},
		{"OEBPS/Text/ch2.xhtml", `<html><body><script>x()</script></body></html>`},
		{"OEBPS/Text/ch3.xhtml", `<html><body><h1>Part 1/2</h1><p>fallback</p></body></html>`},
		{"OEBPS/Text/ch4.xhtml", `<html><body><h1>重复</h1><p>a</p></body></html>`},
		{"OEBPS/Text/ch5.xhtml", `<html><body><h1>重复</h1><p>b</p></body></html>`},
		{"../evil.xhtml", `<html><body><h1>evil</h1></body></html>`},
	})

	code, stdout, stderr := runCLI(t, "extract", "book.epub", "out")
	if code != 0 {
		t.Fatalf("extract exit code = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "5 text file(s)") {
		t.Fatalf("extract stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "unsafe archive path") {
		t.Fatalf("extract stderr = %q", stderr)
	}

	wantFiles := map[string]string{
		"out/OEBPS/Text/第一章.txt": "第一章\n\n正文一。\n\n正文二。",
		"out/OEBPS/Text/ch2.txt": "",
		"out/OEBPS/Text/ch3.txt": "Part 1/2\n\nfallback",
		"out/OEBPS/Text/重复.txt":  "重复\n\na",
		"out/OEBPS/Text/ch5.txt": "重复\n\nb",
	}
	for name, want := range wantFiles {
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if _, err := os.Stat("out/META-INF"); !os.IsNotExist(err) {
		t.Errorf("META-INF should not be extracted, stat err = %v", err)
	}
	if _, err := os.Stat("evil.txt"); !os.IsNotExist(err) {
		t.Errorf("unsafe archive path escaped output directory")
	}

	code, _, stderr = runCLI(t, "extract", "book.epub", "--output", "out2")
	if code != 0 {
		t.Fatalf("extract --output exit code = %d, stderr = %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join("out2", "OEBPS", "Text", "第一章.txt")); err != nil {
		t.Fatalf("--output result missing: %v", err)
	}

	if err := os.WriteFile("book.pdf", []byte("not an epub"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runCLI(t, "extract", "book.pdf")
	if code != 1 {
		t.Fatalf("extract book.pdf exit code = %d, want 1", code)
	}
}
