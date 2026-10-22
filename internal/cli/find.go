package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/tts"
)

func newFindCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "find <file|id> <text>",
		Short: "Search segment text and print matching seq numbers",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runFind(a, args[0], args[1])
			})
		},
	}
}

func runFind(a *app, fileArg, query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return fmt.Errorf("search text must not be empty")
	}
	file, err := resolveFile(a, fileArg)
	if err != nil {
		return err
	}
	segments, err := a.store.SegmentsByFile(file.ID)
	if err != nil {
		return err
	}

	needle := strings.ToLower(query)
	var matches []store.Segment
	for _, seg := range segments {
		if strings.Contains(strings.ToLower(seg.Text), needle) {
			matches = append(matches, seg)
		}
	}

	a.printf("FILE %s (id=%d) matches=%d\n", file.Filename, file.ID, len(matches))
	a.printf("\n")
	printSegmentTableWithText(a.out, matches, func(seg store.Segment) string {
		return matchSnippet(seg.Text, query, 40)
	})
	return nil
}

func matchSnippet(text, query string, width int) string {
	runes := []rune(text)
	lower := []rune(strings.ToLower(text))
	needle := []rune(strings.ToLower(query))
	if len(needle) == 0 || len(lower) != len(runes) {
		return tts.TruncateRunes(text, width)
	}
	idx := runeIndex(lower, needle)
	if idx < 0 {
		return tts.TruncateRunes(text, width)
	}
	if width < len(needle)+10 {
		width = len(needle) + 10
	}

	start := idx - (width-len(needle))/2
	if start < 0 {
		start = 0
	}
	end := start + width
	if end > len(runes) {
		end = len(runes)
		start = end - width
		if start < 0 {
			start = 0
		}
	}

	var b strings.Builder
	if start > 0 {
		b.WriteString("...")
	}
	b.WriteString(string(runes[start:idx]))
	b.WriteByte('[')
	b.WriteString(string(runes[idx : idx+len(needle)]))
	b.WriteByte(']')
	b.WriteString(string(runes[idx+len(needle) : end]))
	if end < len(runes) {
		b.WriteString("...")
	}
	return b.String()
}

func runeIndex(haystack, needle []rune) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
