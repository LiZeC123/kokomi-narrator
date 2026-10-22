package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/tts"
)

func printSegmentTable(w io.Writer, segments []store.Segment) {
	printSegmentTableWithText(w, segments, func(seg store.Segment) string {
		text := tts.TruncateRunes(seg.Text, 30)
		if seg.Status == store.StatusFailed && seg.Error != nil {
			msg := tts.TruncateRunes(strings.Join(strings.Fields(*seg.Error), " "), 50)
			text += fmt.Sprintf("  [error: %s]", msg)
		}
		return text
	})
}

func printSegmentTableWithText(w io.Writer, segments []store.Segment, textFor func(store.Segment) string) {
	fmt.Fprintf(w, "%-5s%-11s%-12s%-10s%s\n", "SEQ", "STATUS", "KIND", "HASH", "TEXT")
	for _, seg := range segments {
		fmt.Fprintf(w, "%-5d%-11s%-12s%-10s%s\n",
			seg.Seq, seg.Status, seg.Kind, shortHash(seg.TextHash), textFor(seg))
	}
}
