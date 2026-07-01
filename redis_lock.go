package go_redis_lock_watchdog

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-redsync/redsync/v4"
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

	delegateMu sync.Mutex
	watchdogMu sync.Mutex

	watchdog *watchdogState
}

type watchdogState struct {
	cancel context.CancelFunc
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
	lock.delegateMu.Lock()
	err := lock.delegate.TryLockContext(ctx)
	lock.delegateMu.Unlock()
	if err != nil {
		return err
	}
	lock.runWatchdog()
	return nil
}

func (lock *redisLock) LockContext(ctx context.Context) error {
	lock.delegateMu.Lock()
	err := lock.delegate.LockContext(ctx)
	lock.delegateMu.Unlock()
	if err != nil {
		return err
	}
	lock.runWatchdog()
	return nil
}

func (lock *redisLock) UnlockContext(ctx context.Context) (bool, error) {
	lock.stopWatchdog()

	lock.delegateMu.Lock()
	defer lock.delegateMu.Unlock()
	return lock.delegate.UnlockContext(ctx)
}

func (lock *redisLock) ExtendContext(ctx context.Context) (bool, error) {
	lock.delegateMu.Lock()
	defer lock.delegateMu.Unlock()
	return lock.delegate.ExtendContext(ctx)
}

func (lock *redisLock) runWatchdog() {
	if lock.watchdogDuration <= 0 {
		return
	}

	lock.stopWatchdog()

	ctx, cancel := context.WithCancel(context.Background())
	watchdog := &watchdogState{cancel: cancel}

	lock.watchdogMu.Lock()
	lock.watchdog = watchdog
	lock.watchdogMu.Unlock()

	go func(ctx context.Context, watchdog *watchdogState) {
		ticker := time.NewTicker(lock.watchdogDuration)
		defer func() {
			ticker.Stop()
			watchdog.cancel()

			lock.watchdogMu.Lock()
			if lock.watchdog == watchdog {
				lock.watchdog = nil
			}
			lock.watchdogMu.Unlock()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				extendCtx, extendCancel := context.WithTimeout(ctx, lock.watchdogDuration)
				ok, err := lock.ExtendContext(extendCtx)
				extendCancel()
				if !ok {
					if ctx.Err() != nil {
						return
					}
					if err != nil && !isLockOwnershipLost(err) {
						lock.logger.Errorf("failed to extend lock with %s: %v", lock.name, err)
						continue
					}
					if err != nil {
						lock.logger.Errorf("failed to extend lock with %s: %v", lock.name, err)
					} else {
						lock.logger.Errorf("failed to extend lock with %s: lock not found", lock.name)
					}
					return
				}
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					lock.logger.Errorf("failed to extend lock with %s: %v", lock.name, err)
					continue
				}
				lock.logger.Debugf("extend lock with %s success", lock.name)
			}
		}
	}(ctx, watchdog)
}

func (lock *redisLock) stopWatchdog() {
	lock.watchdogMu.Lock()
	watchdog := lock.watchdog
	lock.watchdog = nil
	lock.watchdogMu.Unlock()

	if watchdog != nil {
		watchdog.cancel()
	}
}

func isLockOwnershipLost(err error) bool {
	if err == nil {
		return false
	}
	var errTaken *redsync.ErrTaken
	if errors.As(err, &errTaken) {
		return true
	}
	var errNodeTaken *redsync.ErrNodeTaken
	if errors.As(err, &errNodeTaken) {
		return true
	}
	return errors.Is(err, redsync.ErrExtendFailed) ||
		errors.Is(err, redsync.ErrLockAlreadyExpired)
}
