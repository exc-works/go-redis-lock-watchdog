# Go Redis Lock Watchdog

This project provides a Redis-based lock mechanism with a watchdog feature to automatically extend the lock's expiration
time. It is built using Go and leverages the `redsync` library for distributed locks.

## Installation

To install the package, use the following command:

```sh
go get github.com/exc-works/go-redis-lock-watchdog
```

## Usage

### Basic Usage

Here is an example of how to use the Redis lock without the watchdog:

```go
package main

import (
	"context"
	"github.com/go-redis/redis/v9"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	watchdog "github.com/exc-works/go-redis-lock-watchdog"
	redsyncbuilder "github.com/exc-works/go-redis-lock-watchdog/redsync"
	"log"
)

func main() {
	cli := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer cli.Close()

	lock := watchdog.NewRedisLock(
		redsyncbuilder.NewRedisLockBuilder(
			redsync.New(goredis.NewPool(cli)),
			redsync.WithTries(1),
		),
		"test",
	)

	err := lock.TryLockContext(context.TODO())
	if err != nil {
		log.Fatalf("Failed to acquire lock: %v", err)
	}
	defer lock.UnlockContext(context.TODO())

	// Do some work while holding the lock
}
```

### Using Watchdog

Here is an example of how to use the Redis lock with the watchdog:

```go
package main

import (
	"context"
	"github.com/go-redis/redis/v9"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	watchdog "github.com/exc-works/go-redis-lock-watchdog"
	redsyncbuilder "github.com/exc-works/go-redis-lock-watchdog/redsync"
	"log/slog"
	"os"
	"time"
)

func main() {
	cli := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer cli.Close()

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
	if err != nil {
		log.Fatalf("Failed to acquire lock: %v", err)
	}
	defer lock.UnlockContext(context.TODO())

	// Do some work while holding the lock
	time.Sleep(time.Second * 3)
}
```

## Integrating with gocron

This section demonstrates how to integrate the Redis lock mechanism with `gocron` for distributed task scheduling. By
using the Redis lock, you can ensure that only one instance of a scheduled job runs at a time across multiple nodes.

```go
package main

import (
	watchdog "github.com/exc-works/go-redis-lock-watchdog"
	watchdoggocron "github.com/exc-works/go-redis-lock-watchdog/gocron"
	redsyncbuilder "github.com/exc-works/go-redis-lock-watchdog/redsync"
	"github.com/go-co-op/gocron/v2"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
	"log/slog"
	"os"
	"time"
)

func main() {
	cli := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer cli.Close()

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
	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
	}
	defer scheduler.Shutdown()

	_, err = scheduler.NewJob(
		gocron.DurationJob(time.Second),
		gocron.NewTask(func() {
			time.Sleep(time.Second * 5)
		}),
		gocron.WithName("test"),
		gocron.WithStartAt(gocron.WithStartImmediately()),
	)
	if err != nil {
		log.Fatalf("Failed to create job: %v", err)
	}
	scheduler.Start()
}
```
