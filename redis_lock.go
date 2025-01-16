package go_redis_lock_watchdog

import (
	"context"
	"errors"
	"time"
)

type RedisLock interface {
	TryLockContext(context.Context) error

	LockContext(context.Context) error

	UnlockContext(context.Context) (bool, error)

	ExtendContext(context.Context) (bool, error)
}

type RedisLockBuilder func(name string) RedisLock

var _ RedisLock = (*redisLock)(nil)

type redisLock struct {
	delegate RedisLock
	name     string

	logger Logger

	watchdogDuration time.Duration
	watchdogDone     chan struct{}
}

func NewRedisLock(builder RedisLockBuilder, name string) RedisLock {
	return builder(name)
}

func NewRedisLockWithWatchdog(
	builder RedisLockBuilder, name string,
	watchdogDuration time.Duration,
	logger Logger,
) (RedisLock, error) {
	if watchdogDuration <= 0 {
		return nil, errors.New("watchdog duration must be greater than 0")
	}
	if logger == nil {
		logger = NoopLogger{}
	}
	return &redisLock{
		delegate:         builder(name),
		name:             name,
		logger:           logger,
		watchdogDuration: watchdogDuration,
	}, nil
}

func (lock redisLock) TryLockContext(ctx context.Context) error {
	if err := lock.delegate.TryLockContext(ctx); err != nil {
		return err
	}
	lock.runWatchdog(ctx)
	return nil
}

func (lock redisLock) LockContext(ctx context.Context) error {
	if err := lock.delegate.LockContext(ctx); err != nil {
		return err
	}
	lock.runWatchdog(ctx)
	return nil
}

func (lock redisLock) UnlockContext(ctx context.Context) (bool, error) {
	lock.stopWatchdog() // stop watchdog first

	return lock.delegate.UnlockContext(ctx)
}

func (lock redisLock) ExtendContext(ctx context.Context) (bool, error) {
	return lock.delegate.UnlockContext(ctx)
}

func (lock redisLock) runWatchdog(ctx context.Context) {
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

func (lock redisLock) stopWatchdog() {
	close(lock.watchdogDone)
}
