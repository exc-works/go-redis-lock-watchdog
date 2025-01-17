package go_redis_lock_watchdog

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"time"
)

type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

var (
	_ Logger = (*NoopLogger)(nil)
	_ Logger = (*stdLogger)(nil)
)

type NoopLogger struct{}

func (n NoopLogger) Debugf(string, ...any) {
}

func (n NoopLogger) Infof(string, ...any) {
}

func (n NoopLogger) Warnf(string, ...any) {
}

func (n NoopLogger) Errorf(string, ...any) {
}

type stdLogger struct {
	handler slog.Handler
}

func NewStdLogger(handler slog.Handler) Logger {
	return &stdLogger{handler: handler}
}

func (s stdLogger) Debugf(format string, args ...any) {
	s.log(context.Background(), slog.LevelDebug, format, args...)
}

func (s stdLogger) Infof(format string, args ...any) {
	s.log(context.Background(), slog.LevelInfo, format, args...)
}

func (s stdLogger) Warnf(format string, args ...any) {
	s.log(context.Background(), slog.LevelWarn, format, args...)
}

func (s stdLogger) Errorf(format string, args ...any) {
	s.log(context.Background(), slog.LevelError, format, args...)
}

func (s stdLogger) log(ctx context.Context, level slog.Level, format string, args ...any) {
	if !s.handler.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:]) // skip [Callers, log, log's caller]
	r := slog.NewRecord(time.Now(), level, fmt.Sprintf(format, args...), pcs[0])
	_ = s.handler.Handle(ctx, r)
}
