# FrankenAsync

Two-level concurrent PHP: FrankenPHP threads (true parallelism) + Swow coroutines (cooperative concurrency).

## Architecture

- `main.go` — Entry point. Inits FrankenPHP with a configurable thread pool (default 66), auto-prepends `lib/async.php` for Swow coroutine support (unless `FRANKENASYNC_SWOW=0`), includes an inline `/api/comments/{id}` Go endpoint with simulated latency for HTTP mode demos, creates an `http.Handler` that wraps each request with an `asynctask.Manager`, and serves PHP via `frankenphp.ServeHTTP()`.
- `asynctask/` — Go task manager. Provides `Async()`, `Defer()`, `Await()`, `AwaitAll()`, `AwaitAny()`, and `Cancel()`. Uses a semaphore (`WithWorkerLimit`) to limit concurrent goroutines.
- `phpext/` — Minimal C extension registering `Frankenphp\Script` and `Frankenphp\Async\Task` PHP classes. C methods call Go exports via CGO. Go exports access the `asynctask.Manager` from the request context.
- `examples/` — PHP document root.
  - `lib/async.php` — Swow coroutine library (auto-prepended). Provides `async()`, `await()`, `defer()`, and utilities (`race`, `any`, `retry`, `parallel`, `throttle`, `sleep`, `timeout`).
  - `index.php` — Main demo. Splits N tasks into T batches, dispatches each batch as a `Script::async()` to `worker.php`.
  - `include/worker.php` — Coroutine-powered batch subrequest. Receives batch IDs, runs each as a Swow coroutine.
  - `include/task.php` — Single blocking task (pure-thread demo, `coroutines=0`).

## Two-Level Concurrency

```
index.php (1 main thread)
  +- T x Script::async("worker.php")        <- FrankenPHP threads (Level 1: parallelism)
       +- each: M x Swow coroutines          <- within-thread (Level 2: concurrency)
```

**Level 1 — Threads:** `Script::async()` → Go subrequest → separate PHP thread. True parallelism. Works with ANY blocking PHP code.

**Level 2 — Coroutines:** Within each subrequest, Swow `async()`/`await()` fires multiple coroutines. Only works for Swow-hooked I/O. Multiplies concurrency without more threads.

URL params: `?n=100&threads=10&local=1&coroutines=1`

## Swow: Disabling and Multi-Thread Limitation

### Disabling Swow

Swow can be fully disabled via `FRANKENASYNC_SWOW=0` env var (sets `swow.enable=Off` in PHP ini). This disables all Swow hooks at the C level — no transports are registered, no stream ops are proxied. When disabled, `lib/async.php` is not auto-prepended, so only the blocking mode (`coroutines=0`) works.

Use this for vanilla PHP comparison demos. Benchmark result: **blocking mode performs identically with Swow ON or OFF** (~56x for 500 tasks), confirming Swow is transparent when no coroutines are used.

### Multi-Thread Socket Contention

When Swow is enabled, it hooks all stream I/O globally (`file_get_contents`, `usleep`, `stream_socket_client`, etc.):

1. **Single thread**: Swow handles 200+ concurrent HTTP connections perfectly.

2. **Multi-thread**: When multiple FrankenPHP threads (OS threads) make concurrent HTTP calls simultaneously, Swow's internal event infrastructure contends. The practical limit is ~100 total concurrent sockets across all threads.

3. **Chunking workaround**: HTTP mode in `worker.php` chunks coroutines into batches of 10 per thread to stay under this limit (`10 threads × 10 concurrent = 100 total`). Local mock mode (`usleep`) fires all coroutines at once since it doesn't open real sockets.

This is why HTTP mode shows ~66x speedup vs 100x+ for local mock — the chunking serializes network calls into sequential rounds within each thread.

## Build

```bash
make build     # Build the Go binary (dist/frankenasync)
make run       # Build + start the server
make test      # Run asynctask unit tests
make bench     # Build + run automated test suite
```

Build tag: `nowatcher` (required — FrankenPHP's file watcher is not used).

The Go binary requires CGO with PHP headers. Create an `env.yaml` in the project root with `CGO_CFLAGS`, `CGO_CPPFLAGS`, `CGO_LDFLAGS` pointing to your local PHP build. The Makefile requires `env.yaml` to exist before building.

## Key Patterns

### Request Flow

```
HTTP request → main.go handler
  → asynctask.NewManager() (per-request, semaphore-limited)
  → asynctask.WithContext(req.Context(), manager)
  → frankenphp.ServeHTTP()
    → PHP: Script::async() → C: go_execute_script_async() → Go: manager.Async()
    → PHP: Task::awaitAll() → C: go_asynctask_await_all() → Go: manager.AwaitAll()
    → PHP: Swow async()/await() → coroutines within thread (no Go involvement)
  → manager.Shutdown()
```

### Concurrency Model

The semaphore (`WithWorkerLimit`, default 64) controls how many PHP subrequests run simultaneously. Tasks beyond the limit queue in Go goroutines and execute as slots free up (sliding window). Within each subrequest, Swow coroutines provide additional concurrency without consuming threads.

### Thread Pool

FrankenPHP threads are pre-warmed (`WithNumThreads`, default 66) with scaling disabled (`WithMaxThreads == WithNumThreads`). FrankenPHP becomes unstable above ~70 threads (crashes/hangs after repeated requests) — this was observed in recent versions; earlier versions handled higher thread counts. The default of 66 is the tested safe maximum.

The worker semaphore (`FRANKENASYNC_WORKERS`, default 64) is automatically capped at `numThreads - 2` to reserve threads for the main request and overhead. If a higher value is requested via env var, it is capped with a warning log.

## Conventions

- Demo pages go in `examples/`
- Keep `main.go` minimal — this is a demo, not a framework
- The `asynctask/` package has no PHP or FrankenPHP dependencies — it's pure Go
- The `phpext/` package bridges C ↔ Go ↔ FrankenPHP — all PHP class methods live here
- The `lib/async.php` library uses `Frankenphp\` namespace
