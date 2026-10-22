package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

func newLsCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "ls <file|id> [offset] [limit]",
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
	file, err := resolveFile(a, args[0])
	if err != nil {
		return err
	}
	offset, limit := 0, 0
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
	printSegmentTable(a.out, page)
	return nil
}
