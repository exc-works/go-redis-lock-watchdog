package go_redis_lock_watchdog_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/alicebob/miniredis/v2"
	watchdog "github.com/exc-works/go-redis-lock-watchdog"
	redsyncbuilder "github.com/exc-works/go-redis-lock-watchdog/redsync"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/hashicorp/go-multierror"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"log/slog"
	"os"
	"strings"
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

func TestRedisLock_WatchdogContinuesAfterTemporaryExtendError(t *testing.T) {
	delegate := &fakeRedisLock{
		extendResults: []extendResult{
			{ok: false, err: errors.New("temporary redis error")},
			{ok: true},
		},
		extendCalls: make(chan int, 2),
	}
	lock := watchdog.NewRedisLock(
		func(string) watchdog.RedisLock {
			return delegate
		},
		"test-watchdog-temporary-extend-error",
		watchdog.WithWatchdogDuration(20*time.Millisecond),
	)
	require.NoError(t, lock.TryLockContext(context.TODO()))
	defer func() {
		_, _ = lock.UnlockContext(context.TODO())
	}()

	waitExtendCall(t, delegate.extendCalls)
	waitExtendCall(t, delegate.extendCalls)
}

func TestRedisLock_WatchdogLogsExpandedMultiErrorAndStopsOnOwnershipLoss(t *testing.T) {
	logger := &captureLogger{}
	delegate := &fakeRedisLock{
		extendResults: []extendResult{
			{ok: false, err: multierror.Append(nil, &redsync.ErrNodeTaken{Node: 7})},
		},
		extendCalls: make(chan int, 2),
	}
	lock := watchdog.NewRedisLock(
		func(string) watchdog.RedisLock {
			return delegate
		},
		"test-watchdog-expanded-multierror",
		watchdog.WithWatchdogDuration(20*time.Millisecond),
		watchdog.WithLogger(logger),
	)
	require.NoError(t, lock.TryLockContext(context.TODO()))
	defer func() {
		_, _ = lock.UnlockContext(context.TODO())
	}()

	waitExtendCall(t, delegate.extendCalls)
	require.Eventually(t, func() bool {
		message := logger.joinedErrors()
		return strings.Contains(message, "error[0]=") &&
			strings.Contains(message, "redsync.ErrNodeTaken") &&
			strings.Contains(message, "node=7")
	}, 200*time.Millisecond, 10*time.Millisecond)

	select {
	case <-delegate.extendCalls:
		t.Fatal("watchdog continued after ownership loss")
	case <-time.After(80 * time.Millisecond):
	}
}

func TestRedisLock_WatchdogLogsEmptyChildErrorMessage(t *testing.T) {
	logger := &captureLogger{}
	delegate := &fakeRedisLock{
		extendResults: []extendResult{
			{ok: false, err: multierror.Append(nil, emptyExtendError{})},
			{ok: true},
		},
		extendCalls: make(chan int, 2),
	}
	lock := watchdog.NewRedisLock(
		func(string) watchdog.RedisLock {
			return delegate
		},
		"test-watchdog-empty-child-error",
		watchdog.WithWatchdogDuration(20*time.Millisecond),
		watchdog.WithLogger(logger),
	)
	require.NoError(t, lock.TryLockContext(context.TODO()))
	defer func() {
		_, _ = lock.UnlockContext(context.TODO())
	}()

	waitExtendCall(t, delegate.extendCalls)
	waitExtendCall(t, delegate.extendCalls)
	require.Eventually(t, func() bool {
		message := logger.joinedErrors()
		return strings.Contains(message, "error[0]=") &&
			strings.Contains(message, "emptyExtendError") &&
			strings.Contains(message, "<empty error message>")
	}, 200*time.Millisecond, 10*time.Millisecond)
}

func TestRedisLock_WatchdogLogsRedisErrorNodeAndInnerMessage(t *testing.T) {
	logger := &captureLogger{}
	delegate := &fakeRedisLock{
		extendResults: []extendResult{
			{ok: false, err: multierror.Append(nil, &redsync.RedisError{Node: 3, Err: emptyExtendError{}})},
			{ok: true},
		},
		extendCalls: make(chan int, 2),
	}
	lock := watchdog.NewRedisLock(
		func(string) watchdog.RedisLock {
			return delegate
		},
		"test-watchdog-redis-error",
		watchdog.WithWatchdogDuration(20*time.Millisecond),
		watchdog.WithLogger(logger),
	)
	require.NoError(t, lock.TryLockContext(context.TODO()))
	defer func() {
		_, _ = lock.UnlockContext(context.TODO())
	}()

	waitExtendCall(t, delegate.extendCalls)
	waitExtendCall(t, delegate.extendCalls)
	require.Eventually(t, func() bool {
		message := logger.joinedErrors()
		return strings.Contains(message, "redsync.RedisError") &&
			strings.Contains(message, "node=3") &&
			strings.Contains(message, "<empty error message>")
	}, 200*time.Millisecond, 10*time.Millisecond)
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

type extendResult struct {
	ok  bool
	err error
}

type fakeRedisLock struct {
	mu            sync.Mutex
	extendResults []extendResult
	extendCalls   chan int
}

func (f *fakeRedisLock) TryLockContext(context.Context) error {
	return nil
}

func (f *fakeRedisLock) LockContext(context.Context) error {
	return nil
}

func (f *fakeRedisLock) UnlockContext(context.Context) (bool, error) {
	return true, nil
}

func (f *fakeRedisLock) ExtendContext(context.Context) (bool, error) {
	f.mu.Lock()
	call := len(f.extendResults)
	result := extendResult{ok: true}
	if call > 0 {
		result = f.extendResults[0]
		f.extendResults = f.extendResults[1:]
	}
	f.mu.Unlock()

	f.extendCalls <- call
	return result.ok, result.err
}

func waitExtendCall(t *testing.T, calls <-chan int) {
	t.Helper()

	select {
	case <-calls:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for watchdog extend call")
	}
}

type emptyExtendError struct{}

func (emptyExtendError) Error() string {
	return ""
}

type captureLogger struct {
	mu     sync.Mutex
	errors []string
}

func (c *captureLogger) Debugf(string, ...any) {}

func (c *captureLogger) Infof(string, ...any) {}

func (c *captureLogger) Warnf(string, ...any) {}

func (c *captureLogger) Errorf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, fmt.Sprintf(format, args...))
}

func (c *captureLogger) joinedErrors() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.errors, "\n")
}
