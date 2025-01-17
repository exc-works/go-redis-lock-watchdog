package gocron_test

import (
	"github.com/alicebob/miniredis/v2"
	watchdog "github.com/exc-works/go-redis-lock-watchdog"
	watchdoggocron "github.com/exc-works/go-redis-lock-watchdog/gocron"
	redsyncbuilder "github.com/exc-works/go-redis-lock-watchdog/redsync"
	"github.com/go-co-op/gocron/v2"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocker(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	var count int32 = 0

	fn := func() gocron.Scheduler {
		cli := redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
		})
		scheduler, err := gocron.NewScheduler(
			gocron.WithDistributedLocker(
				watchdoggocron.NewRedisLocker(
					redsyncbuilder.NewRedisLockBuilder(
						redsync.New(goredis.NewPool(cli)),
						redsync.WithTries(1),
						redsync.WithExpiry(time.Second*2),
					),
					watchdog.WithWatchdogDuration(time.Second),
					watchdog.WithLogger(watchdog.NewStdLogger(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
						AddSource: true,
						Level:     slog.LevelDebug,
					}))),
				),
			),
		)
		require.NoError(t, err)

		_, err = scheduler.NewJob(
			gocron.DurationJob(time.Second),
			gocron.NewTask(func() {
				atomic.AddInt32(&count, 1)
				time.Sleep(time.Second * 5)
			}),
			gocron.WithName("test"),
			gocron.WithStartAt(gocron.WithStartImmediately()),
		)
		require.NoError(t, err)
		scheduler.Start()
		return scheduler
	}

	for i := 0; i < 5; i++ {
		go func() {
			scheduler := fn()
			defer scheduler.Shutdown()

			time.Sleep(time.Second * 4)
		}()
	}

	time.Sleep(time.Second * 5)

	require.Equal(t, int32(1), atomic.LoadInt32(&count))
}
