package go_redis_lock_watchdog

type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

var _ Logger = (*NoopLogger)(nil)

type NoopLogger struct{}

func (n NoopLogger) Debugf(string, ...any) {
}

func (n NoopLogger) Infof(string, ...any) {
}

func (n NoopLogger) Warnf(string, ...any) {
}

func (n NoopLogger) Errorf(string, ...any) {
}
