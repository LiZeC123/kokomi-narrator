package cli

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/config"
	"github.com/LiZeC123/kokomi-narrator/internal/store"
)

type app struct {
	cfg    *config.Config
	store  *store.Store
	log    *slog.Logger
	out    io.Writer
	errOut io.Writer
}

func withApp(cmd *cobra.Command, configPath string, fn func(*app) error) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.Database)
	if err != nil {
		return err
	}
	defer st.Close()

	a := &app{
		cfg:    cfg,
		store:  st,
		log:    newLogger(cmd.ErrOrStderr(), cfg.LogLevel),
		out:    cmd.OutOrStdout(),
		errOut: cmd.ErrOrStderr(),
	}
	slog.SetDefault(a.log)
	return fn(a)
}

func newLogger(w io.Writer, level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lv}))
}

func (a *app) printf(format string, args ...any) {
	fmt.Fprintf(a.out, format, args...)
}

func (a *app) errorf(format string, args ...any) {
	fmt.Fprintf(a.errOut, format, args...)
}

func (a *app) timestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
