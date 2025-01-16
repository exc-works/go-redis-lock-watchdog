package go_redis_lock_watchdog_test

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	watchdog "github.com/exc-works/go-redis-lock-watchdog"
	redsyncbuilder "github.com/exc-works/go-redis-lock-watchdog/redsync"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestRedisLock_TryLockContext(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	cli := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer cli.Close()

	t.Run("without watchdog", func(t *testing.T) {
		lock := watchdog.NewRedisLock(
			redsyncbuilder.NewRedisLockBuilder(
				redsync.New(goredis.NewPool(cli)),
				redsync.WithTries(1),
			),
			"test",
		)
		err := lock.TryLockContext(context.TODO())
		require.NoError(t, err)
		defer lock.UnlockContext(context.TODO())
		err = lock.TryLockContext(context.TODO())
		require.Error(t, err)
	})

	t.Run("with watchdog", func(t *testing.T) {
		lock := watchdog.NewRedisLock(
			redsyncbuilder.NewRedisLockBuilder(
				redsync.New(goredis.NewPool(cli)),
				redsync.WithTries(1),
				redsync.WithExpiry(time.Second*2),
			),
			"test-watchdog",
			watchdog.WithWatchdogDuration(time.Second),
			watchdog.WithLogger(watchdog.NewStdLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			})))),
		)
		err := lock.TryLockContext(context.TODO())
		require.NoError(t, err)
		err = lock.TryLockContext(context.TODO())
		require.Error(t, err)
		time.Sleep(time.Second * 3)
		err = lock.TryLockContext(context.TODO())
		require.Error(t, err)

		ok, err := lock.UnlockContext(context.TODO())
		require.NoError(t, err)
		require.True(t, ok)

		err = lock.TryLockContext(context.TODO())
		require.NoError(t, err)
		ok, err = lock.UnlockContext(context.TODO())
		require.NoError(t, err)
		require.True(t, ok)
	})
}
