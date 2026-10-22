package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"

	"github.com/LiZeC123/kokomi-narrator/internal/tts"
)

type progress struct {
	mu       sync.Mutex
	w        io.Writer
	total    int
	done     int
	failed   int
	bar      *progressbar.ProgressBar
	terminal bool
}

func newProgress(w io.Writer, total int) *progress {
	p := &progress{
		w:        w,
		total:    total,
		terminal: isTerminal(w),
	}
	if p.terminal && total > 0 {
		p.bar = progressbar.NewOptions(total,
			progressbar.OptionSetWriter(w),
			progressbar.OptionSetDescription("tts"),
			progressbar.OptionSetRenderBlankState(true),
			progressbar.OptionShowCount(),
			progressbar.OptionShowIts(),
			progressbar.OptionSetWidth(24),
			progressbar.OptionThrottle(100*time.Millisecond),
		)
	}
	return p
}

func (p *progress) starting(hash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bar == nil {
		return
	}
	p.bar.Describe(fmt.Sprintf("tts failed=%d hash=%s", p.failed, shortHash(hash)))
}

func (p *progress) complete(hash string, segments int, failed bool, detail string) {
	if segments <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += segments
	if failed {
		p.failed += segments
	}

	if p.bar != nil {
		p.bar.Describe(fmt.Sprintf("tts failed=%d hash=%s", p.failed, shortHash(hash)))
		_ = p.bar.Add(segments)
		return
	}

	status := "success"
	if failed {
		status = "failed"
	}
	line := fmt.Sprintf("[%d/%d] %s %s (%d segment(s))",
		p.done, p.total, shortHash(hash), status, segments)
	if failed && detail != "" {
		line += ": " + tts.TruncateRunes(strings.Join(strings.Fields(detail), " "), 100)
	}
	fmt.Fprintln(p.w, line)
}

func (p *progress) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bar != nil {
		_ = p.bar.Exit()
		fmt.Fprintln(p.w)
	}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
