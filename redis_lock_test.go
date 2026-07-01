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
	"sync"
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
			watchdog.WithLogger(watchdog.NewStdLogger(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			}))),
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

func TestRedisLock_ExtendContext(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	cli := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer cli.Close()

	builder := redsyncbuilder.NewRedisLockBuilder(
		redsync.New(goredis.NewPool(cli)),
		redsync.WithTries(1),
		redsync.WithExpiry(500*time.Millisecond),
	)
	lock := watchdog.NewRedisLock(builder, "test-extend")
	require.NoError(t, lock.TryLockContext(context.TODO()))
	defer func() {
		_, _ = lock.UnlockContext(context.TODO())
	}()

	time.Sleep(350 * time.Millisecond)

	ok, err := lock.ExtendContext(context.TODO())
	require.NoError(t, err)
	require.True(t, ok)

	time.Sleep(250 * time.Millisecond)

	contender := watchdog.NewRedisLock(builder, "test-extend")
	require.Error(t, contender.TryLockContext(context.TODO()))
}

func TestRedisLock_WatchdogKeepsLockAcrossMultipleTicks(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	cli := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer cli.Close()

	builder := redsyncbuilder.NewRedisLockBuilder(
		redsync.New(goredis.NewPool(cli)),
		redsync.WithTries(1),
		redsync.WithExpiry(200*time.Millisecond),
	)
	lock := watchdog.NewRedisLock(
		builder,
		"test-watchdog-multiple-ticks",
		watchdog.WithWatchdogDuration(50*time.Millisecond),
	)
	require.NoError(t, lock.TryLockContext(context.TODO()))
	defer func() {
		_, _ = lock.UnlockContext(context.TODO())
	}()

	time.Sleep(550 * time.Millisecond)

	contender := watchdog.NewRedisLock(builder, "test-watchdog-multiple-ticks")
	require.Error(t, contender.TryLockContext(context.TODO()))
}

func TestRedisLock_WatchdogStopsAfterLockValueChanges(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	cli := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer cli.Close()

	const lockName = "test-watchdog-stops-after-value-change"
	builder := redsyncbuilder.NewRedisLockBuilder(
		redsync.New(goredis.NewPool(cli)),
		redsync.WithTries(1),
		redsync.WithExpiry(200*time.Millisecond),
		redsync.WithSetNXOnExtend(),
	)
	lock := watchdog.NewRedisLock(
		builder,
		lockName,
		watchdog.WithWatchdogDuration(50*time.Millisecond),
	)
	require.NoError(t, lock.TryLockContext(context.TODO()))
	defer func() {
		_, _ = lock.UnlockContext(context.TODO())
	}()

	require.NoError(t, cli.Set(context.TODO(), lockName, "another-owner", time.Second).Err())
	time.Sleep(120 * time.Millisecond)
	require.NoError(t, cli.Del(context.TODO(), lockName).Err())
	time.Sleep(120 * time.Millisecond)

	contender := watchdog.NewRedisLock(builder, lockName)
	require.NoError(t, contender.TryLockContext(context.TODO()))
	_, _ = contender.UnlockContext(context.TODO())
}

func TestRedisLock_WatchdogOutlivesLockContext(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	cli := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer cli.Close()

	builder := redsyncbuilder.NewRedisLockBuilder(
		redsync.New(goredis.NewPool(cli)),
		redsync.WithTries(1),
		redsync.WithExpiry(200*time.Millisecond),
	)
	lockCtx, cancel := context.WithCancel(context.Background())
	lock := watchdog.NewRedisLock(
		builder,
		"test-watchdog-outlives-lock-context",
		watchdog.WithWatchdogDuration(50*time.Millisecond),
	)
	require.NoError(t, lock.TryLockContext(lockCtx))
	defer func() {
		_, _ = lock.UnlockContext(context.TODO())
	}()

	cancel()
	time.Sleep(550 * time.Millisecond)

	contender := watchdog.NewRedisLock(builder, "test-watchdog-outlives-lock-context")
	require.Error(t, contender.TryLockContext(context.TODO()))
}

func TestRedisLock_UnlockStopsWatchdogAndAllowsRelock(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	cli := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer cli.Close()

	builder := redsyncbuilder.NewRedisLockBuilder(
		redsync.New(goredis.NewPool(cli)),
		redsync.WithTries(1),
		redsync.WithExpiry(200*time.Millisecond),
	)
	lock := watchdog.NewRedisLock(
		builder,
		"test-watchdog-unlock",
		watchdog.WithWatchdogDuration(50*time.Millisecond),
	)
	require.NoError(t, lock.TryLockContext(context.TODO()))

	ok, err := lock.UnlockContext(context.TODO())
	require.NoError(t, err)
	require.True(t, ok)

	time.Sleep(150 * time.Millisecond)

	contender := watchdog.NewRedisLock(builder, "test-watchdog-unlock")
	require.NoError(t, contender.TryLockContext(context.TODO()))
	_, _ = contender.UnlockContext(context.TODO())
}

func TestRedisLock_CanReuseSameInstanceAfterUnlock(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	cli := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer cli.Close()

	builder := redsyncbuilder.NewRedisLockBuilder(
		redsync.New(goredis.NewPool(cli)),
		redsync.WithTries(1),
		redsync.WithExpiry(200*time.Millisecond),
	)
	lock := watchdog.NewRedisLock(
		builder,
		"test-watchdog-reuse",
		watchdog.WithWatchdogDuration(50*time.Millisecond),
	)
	require.NoError(t, lock.TryLockContext(context.TODO()))
	ok, err := lock.UnlockContext(context.TODO())
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, lock.TryLockContext(context.TODO()))
	defer func() {
		_, _ = lock.UnlockContext(context.TODO())
	}()

	time.Sleep(550 * time.Millisecond)

	contender := watchdog.NewRedisLock(builder, "test-watchdog-reuse")
	require.Error(t, contender.TryLockContext(context.TODO()))
}

func TestRedisLock_ConcurrentUnlockDoesNotPanic(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	cli := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer cli.Close()

	builder := redsyncbuilder.NewRedisLockBuilder(
		redsync.New(goredis.NewPool(cli)),
		redsync.WithTries(1),
		redsync.WithExpiry(200*time.Millisecond),
	)
	lock := watchdog.NewRedisLock(
		builder,
		"test-watchdog-concurrent-unlock",
		watchdog.WithWatchdogDuration(50*time.Millisecond),
	)
	require.NoError(t, lock.TryLockContext(context.TODO()))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = lock.UnlockContext(context.TODO())
		}()
	}
	wg.Wait()
}
