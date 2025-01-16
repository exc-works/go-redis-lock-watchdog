package go_redis_lock_watchdog

import (
	"context"
	"time"
)

type RedisLock interface {
	// TryLockContext only attempts to lock m once and returns immediately regardless of success or failure without retrying.
	TryLockContext(context.Context) error

	// LockContext locks the key. In case it returns an error on failure, you may retry to acquire the lock by calling this method again.
	LockContext(context.Context) error

	// UnlockContext unlocks and returns the status of unlock.
	UnlockContext(context.Context) (bool, error)

	// ExtendContext resets the lock's expiry and returns the status of expiry extension.
	ExtendContext(context.Context) (bool, error)
}

type RedisLockBuilder func(name string) RedisLock

var _ RedisLock = (*redisLock)(nil)

type Option func(*redisLock)

func WithWatchdogDuration(duration time.Duration) Option {
	return func(lock *redisLock) {
		lock.watchdogDuration = duration
	}
}

func WithLogger(logger Logger) Option {
	return func(lock *redisLock) {
		lock.logger = logger
	}
}

type redisLock struct {
	delegate RedisLock
	name     string

	logger Logger

	watchdogDuration time.Duration
	watchdogDone     chan struct{}
}

// NewRedisLock creates a new RedisLock with the given name and options.
func NewRedisLock(
	builder RedisLockBuilder, name string, opts ...Option,
) RedisLock {
	r := &redisLock{
		delegate: builder(name),
		name:     name,
		logger:   &NoopLogger{},
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (lock *redisLock) TryLockContext(ctx context.Context) error {
	if err := lock.delegate.TryLockContext(ctx); err != nil {
		return err
	}
	lock.runWatchdog(ctx)
	return nil
}

func (lock *redisLock) LockContext(ctx context.Context) error {
	if err := lock.delegate.LockContext(ctx); err != nil {
		return err
	}
	lock.runWatchdog(ctx)
	return nil
}

func (lock *redisLock) UnlockContext(ctx context.Context) (bool, error) {
	lock.stopWatchdog() // stop watchdog first

	return lock.delegate.UnlockContext(ctx)
}

func (lock *redisLock) ExtendContext(ctx context.Context) (bool, error) {
	return lock.delegate.UnlockContext(ctx)
}

func (lock *redisLock) runWatchdog(ctx context.Context) {
	if lock.watchdogDuration <= 0 {
		return
	}

	lock.watchdogDone = make(chan struct{})
	go func() {
		ticker := time.NewTicker(lock.watchdogDuration)
		defer ticker.Stop()
		select {
		case <-lock.watchdogDone:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := lock.delegate.ExtendContext(ctx)
			if err != nil {
				lock.logger.Errorf("failed to extend lock with %s: %v", lock.name, err)
			} else if !ok {
				lock.logger.Errorf("failed to extend lock with %s: lock not found", lock.name)
				return
			} else {
				lock.logger.Debugf("extend lock with %s success", lock.name)
			}
		}
	}()
}

func (lock *redisLock) stopWatchdog() {
	if lock.watchdogDone != nil {
		close(lock.watchdogDone)
	}
}
