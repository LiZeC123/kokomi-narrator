package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/tts"
)

func newLsCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "ls <file> [offset] [limit]",
		Short: "List segments of a text file",
		Args:  cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runLs(a, args)
			})
		},
	}
}

func runLs(a *app, args []string) error {
	name := args[0]
	if err := validateFilename(name); err != nil {
		return err
	}
	offset, limit := 0, 0
	var err error
	if len(args) > 1 {
		offset, err = strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid offset %q: %w", args[1], err)
		}
	}
	if len(args) > 2 {
		limit, err = strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("invalid limit %q: %w", args[2], err)
		}
	}
	if offset < 0 {
		return fmt.Errorf("offset must not be negative")
	}
	if limit < 0 {
		return fmt.Errorf("limit must not be negative")
	}

	file, err := a.store.FileByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%s is not registered (run add first)", name)
		}
		return err
	}
	segments, err := a.store.SegmentsByFile(file.ID)
	if err != nil {
		return err
	}

	total := len(segments)
	if offset > total {
		offset = total
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	page := segments[offset:end]

	a.printf("FILE %s total=%d voice=%s\n", file.Filename, total, file.VoiceKey)
	if len(page) == 0 {
		a.printf("showing 0-0 of %d\n", total)
	} else {
		a.printf("showing %d-%d of %d\n", offset, offset+len(page)-1, total)
	}
	a.printf("\n")
	a.printf("%-5s%-11s%-12s%-10s%s\n", "SEQ", "STATUS", "KIND", "HASH", "TEXT")

	for _, seg := range page {
		text := tts.TruncateRunes(seg.Text, 30)
		if seg.Status == store.StatusFailed && seg.Error != nil {
			msg := tts.TruncateRunes(strings.Join(strings.Fields(*seg.Error), " "), 50)
			text += fmt.Sprintf("  [error: %s]", msg)
		}
		a.printf("%-5d%-11s%-12s%-10s%s\n", seg.Seq, seg.Status, seg.Kind, shortHash(seg.TextHash), text)
	}
	return nil
}
