package cli

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/net/html"
)

const defaultExtractDir = "plain_text"

func newExtractCommand() *cobra.Command {
	var outputFlag string

	cmd := &cobra.Command{
		Use:     "extract <file.epub> [output_dir]",
		Aliases: []string{"epub"},
		Short:   "Extract EPUB chapters into text files",
		Long: "Extract every HTML/XHTML chapter from an EPUB archive into .txt files.\n" +
			"Files keep their original directory structure. Each output file is named\n" +
			"after the first non-empty line of its text, falling back to the original\n" +
			"file name when the text is empty or the line cannot be used as a filename.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			outputDir := outputFlag
			if len(args) == 2 {
				if cmd.Flags().Changed("output") {
					return fmt.Errorf("specify the output directory either as an argument or with --output, not both")
				}
				outputDir = args[1]
			}
			if strings.TrimSpace(outputDir) == "" {
				return fmt.Errorf("output directory must not be empty")
			}
			return runExtract(cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], outputDir)
		},
	}
	cmd.Flags().StringVarP(&outputFlag, "output", "o", defaultExtractDir, "directory for extracted .txt files")
	return cmd
}

func runExtract(stdout, stderr io.Writer, epubName, outputDir string) error {
	if err := validateEpubFilename(epubName); err != nil {
		return err
	}
	if err := ensureRegularFile(epubName); err != nil {
		return err
	}

	r, err := zip.OpenReader(epubName)
	if err != nil {
		return fmt.Errorf("open %s: %w", epubName, err)
	}
	defer r.Close()

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory %s: %w", outputDir, err)
	}

	used := make(map[string]bool)
	extracted, failed := 0, 0

	for _, f := range r.File {
		rel, ok := cleanZipPath(f.Name)
		if !ok {
			fmt.Fprintf(stderr, "warning: skipping %s (unsafe archive path)\n", f.Name)
			failed++
			continue
		}
		if f.FileInfo().IsDir() {
			continue
		}

		ext := strings.ToLower(path.Ext(rel))
		if ext != ".html" && ext != ".xhtml" && ext != ".htm" {
			continue
		}

		text, err := extractZipHTML(f)
		if err != nil {
			fmt.Fprintf(stderr, "warning: extract %s: %v\n", f.Name, err)
			failed++
			continue
		}

		dir := path.Dir(rel)
		name := outputFilename(dir, text, originalBase(rel), used)
		outPath := filepath.Join(outputDir, filepath.FromSlash(dir), name)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			fmt.Fprintf(stderr, "warning: create directory %s: %v\n", filepath.Dir(outPath), err)
			failed++
			continue
		}
		if err := os.WriteFile(outPath, []byte(text), 0o644); err != nil {
			fmt.Fprintf(stderr, "warning: write %s: %v\n", outPath, err)
			failed++
			continue
		}
		fmt.Fprintf(stdout, "extracted %s -> %s\n", rel, outPath)
		extracted++
	}

	if extracted == 0 && failed > 0 {
		return fmt.Errorf("failed to extract text from %s", epubName)
	}
	fmt.Fprintf(stdout, "extract %s: %d text file(s) -> %s\n", epubName, extracted, outputDir)
	return nil
}

func extractZipHTML(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	return extractHTMLText(rc)
}

func extractHTMLText(r io.Reader) (string, error) {
	root, err := html.Parse(r)
	if err != nil {
		return "", err
	}

	start := findElement(root, "body")
	if start == nil {
		start = root
	}

	var b strings.Builder
	walkHTML(&b, start)
	return normalizeHTMLText(b.String()), nil
}

func findElement(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, tag); found != nil {
			return found
		}
	}
	return nil
}

func walkHTML(b *strings.Builder, n *html.Node) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "head", "nav", "noscript", "template":
			return
		case "br":
			b.WriteByte('\n')
			return
		}
	}

	if n.Type == html.TextNode {
		b.WriteString(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTML(b, c)
	}
	if n.Type == html.ElementNode && isBlockElement(n.Data) {
		b.WriteByte('\n')
	}
}

func isBlockElement(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "dd", "div", "dl", "dt",
		"figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5",
		"h6", "header", "hr", "li", "main", "ol", "p", "pre", "section", "table",
		"tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

func normalizeHTMLText(s string) string {
	lines := strings.Split(s, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n\n")
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func usableTitleFilename(line string) (string, bool) {
	name := strings.TrimSpace(line)
	if name == "" || name == "." || name == ".." {
		return "", false
	}
	if strings.HasSuffix(name, ".") {
		return "", false
	}
	if strings.ContainsAny(name, `/\`) {
		return "", false
	}
	for _, r := range name {
		if r == 0 || r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	// Keep well below the common 255-byte filename limit.
	if len(name)+len(".txt") > 200 {
		return "", false
	}
	return name, true
}

// outputFilename prefers the first non-empty line, and falls back to the
// original EPUB basename when the text is empty or the line is unusable.
func outputFilename(dir, text, fallback string, used map[string]bool) string {
	if line := firstNonEmptyLine(text); line != "" {
		if name, ok := usableTitleFilename(line); ok {
			key := filenameKey(dir, name+".txt")
			if !used[key] {
				used[key] = true
				return name + ".txt"
			}
		}
	}
	return claimFallbackFilename(dir, fallback, used)
}

func claimFallbackFilename(dir, base string, used map[string]bool) string {
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == ".." {
		base = "untitled"
	}
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		key := filenameKey(dir, name+".txt")
		if !used[key] {
			used[key] = true
			return name + ".txt"
		}
	}
}

func filenameKey(dir, name string) string {
	// macOS file systems are usually case-insensitive; avoid silent overwrites.
	return strings.ToLower(path.Join(dir, name))
}

func originalBase(rel string) string {
	base := path.Base(rel)
	base = strings.TrimSuffix(base, path.Ext(base))
	if base == "" || base == "." || base == ".." {
		return "untitled"
	}
	return base
}

func cleanZipPath(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", false
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." {
			return "", false
		}
	}
	return cleaned, true
}
