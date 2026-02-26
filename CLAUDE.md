# FrankenAsync

Concurrent PHP via FrankenPHP threads (true parallelism) with a Go semaphore sliding window.

## Architecture

- `main.go` — Entry point. Inits FrankenPHP with a configurable thread pool (default 4x CPU cores), includes an inline `/api/comments/{id}` Go endpoint with simulated latency for HTTP mode demos, creates an `http.Handler` that wraps each request with an `asynctask.Manager`, and serves PHP via `frankenphp.ServeHTTP()`. Swow is hardcoded to disabled (`swow.enable=0`).
- `asynctask/` — Go task manager. Provides `Async()`, `Defer()`, `Await()`, `AwaitAll()`, `AwaitAny()`, and `Cancel()`. Uses a semaphore (`WithWorkerLimit`) to limit concurrent goroutines.
- `phpext/` — Minimal C extension registering `Frankenphp\Script` and `Frankenphp\Async\Future` PHP classes. C methods call Go exports via CGO. Go exports access the `asynctask.Manager` from the request context.
- `examples/` — PHP document root.
  - `index.php` — Main demo. Dispatches N tasks as `Script::async()` calls to `task.php`, Go semaphore handles the sliding window.
  - `include/task.php` — Single blocking task (simulated or real HTTP I/O).
  - `lib/async.php` — Structured concurrency helpers: `race()`, `retry()`, `parallel()`, `throttle()`. Plain PHP generators composing on `Script::async()` and `Future` — no Swow, no coroutines.

## Concurrency Model

```
index.php (1 main thread)
  +- N x Script::async("task.php")     <- FrankenPHP threads (true parallelism)
       +- Go semaphore sliding window  <- queues tasks beyond worker limit
```

`Script::async()` dispatches each PHP script to a separate FrankenPHP thread. True parallelism. Works with ANY blocking PHP code. The Go semaphore (`WithWorkerLimit`) controls how many subrequests run simultaneously — tasks beyond the limit queue and execute as slots free up.

URL params: `?n=100&local=1`

## Build

```bash
make build     # Build the Go binary (dist/frankenasync)
make run       # Build + start the server
make test      # Run asynctask unit tests
make bench     # Build + run automated test suite
```

Build tag: `nowatcher` (required — FrankenPHP's file watcher is not used).

The Go binary requires CGO with PHP headers. Build PHP and generate `env.yaml`:

```bash
make php       # Build PHP 8.3 via static-php-cli (ZTS + embed, no Swow)
make env       # Generate env.yaml from the PHP build
```

Or create `env.yaml` manually with `CGO_CFLAGS`, `CGO_CPPFLAGS`, `CGO_LDFLAGS` pointing to your local PHP build.

## Key Patterns

### Request Flow

```
HTTP request → main.go handler
  → asynctask.NewManager() (per-request, semaphore-limited)
  → asynctask.WithContext(req.Context(), manager)
  → frankenphp.ServeHTTP()
    → PHP: Script::async() → C: go_execute_script_async() → Go: manager.Async()
    → PHP: Future::awaitAll() → C: go_asynctask_await_all() → Go: manager.AwaitAll()
  → manager.Shutdown()
```

### Thread Pool

FrankenPHP threads are pre-warmed (`WithNumThreads`, default 4x CPU cores) with scaling disabled (`WithMaxThreads == WithNumThreads`). The worker semaphore (`FRANKENASYNC_WORKERS`, default `numThreads - 2`) is automatically capped at `numThreads - 2` to reserve threads for the main request and overhead.

## Conventions

- Demo pages go in `examples/`
- Keep `main.go` minimal — this is a demo, not a framework
- The `asynctask/` package has no PHP or FrankenPHP dependencies — it's pure Go
- The `phpext/` package bridges C ↔ Go ↔ FrankenPHP — all PHP class methods live here
