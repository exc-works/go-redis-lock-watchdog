package gocron

import (
	"context"
	"fmt"
	watchdog "github.com/exc-works/go-redis-lock-watchdog"
	"github.com/go-co-op/gocron/v2"
)

var (
	ErrLockNotAcquired = fmt.Errorf("lock not acquired")
)

var _ gocron.Locker = (*locker)(nil)

type locker struct {
	builder watchdog.RedisLockBuilder
	opts    []watchdog.Option
}

func NewRedisLocker(builder watchdog.RedisLockBuilder, opts ...watchdog.Option) gocron.Locker {
	return &locker{builder: builder, opts: opts}
}

func (l *locker) Lock(ctx context.Context, key string) (gocron.Lock, error) {
	adapter := &lockAdapter{lock: watchdog.NewRedisLock(l.builder, key, l.opts...)}
	err := adapter.lock.TryLockContext(ctx)
	if err != nil {
		return nil, err
	}
	return adapter, nil
}

var _ gocron.Lock = (*lockAdapter)(nil)

type lockAdapter struct {
	lock watchdog.RedisLock
}

func (la *lockAdapter) Unlock(ctx context.Context) error {
	ok, err := la.lock.UnlockContext(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrLockNotAcquired
	}
	return nil
}
