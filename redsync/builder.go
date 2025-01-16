package redsync

import (
	watchdog "github.com/exc-works/go-redis-lock-watchdog"
	"github.com/go-redsync/redsync/v4"
)

func NewRedisLockBuilder(rs *redsync.Redsync, options ...redsync.Option) watchdog.RedisLockBuilder {
	return func(name string) watchdog.RedisLock {
		return rs.NewMutex(name, options...)
	}
}
