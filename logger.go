package go_redis_lock_watchdog

import (
	"context"
	"fmt"
	"log/slog"
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
	logger *slog.Logger
}

func NewStdLogger(logger *slog.Logger) Logger {
	return &stdLogger{logger: logger}
}

func (s stdLogger) Debugf(format string, args ...any) {
	s.logger.Log(context.TODO(), slog.LevelDebug, fmt.Sprintf(format, args...))

}

func (s stdLogger) Infof(format string, args ...any) {
	s.logger.Log(context.TODO(), slog.LevelInfo, fmt.Sprintf(format, args...))
}

func (s stdLogger) Warnf(format string, args ...any) {
	s.logger.Log(context.TODO(), slog.LevelWarn, fmt.Sprintf(format, args...))
}

func (s stdLogger) Errorf(format string, args ...any) {
	s.logger.Log(context.TODO(), slog.LevelError, fmt.Sprintf(format, args...))
}
