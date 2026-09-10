package logging

import (
	"log/slog"
	"os"
)

// NewHandler returns the process-wide JSON slog handler.
func NewHandler() slog.Handler {
	return slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
}

// Init sets the default logger to NewHandler.
func Init() {
	slog.SetDefault(slog.New(NewHandler()))
}
