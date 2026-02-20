# FrankenAsync

Two-level concurrent PHP — [FrankenPHP](https://frankenphp.dev) threads + [Swow](https://github.com/swow/swow) coroutines, 100x+ speedup with standard blocking PHP code.

FrankenAsync combines FrankenPHP threads (true parallelism for any PHP code) with Swow coroutines (cooperative concurrency within each thread). 10 threads x 50 coroutines = 500 concurrent tasks with only 10 actual threads.

![FrankenAsync Demo](screenshot.png)

> **Note**: This is a companion repo for my FrankenPHP conference talks. It's meant as inspiration and a reference implementation, not a production framework. Feel free to explore, fork, and adapt the patterns for your own projects.

### Talks

- [PHP 150x Faster, Still Legacy-Friendly](https://confoo.ca/en/2026/session/php-150x-faster-still-legacy-friendly) — ConFoo 2026
- [php[tek] 2026](https://phptek.io/) — Chicago, May 19-21

## Two-Level Concurrency

**Level 1 — Threads:** `Script::async()` dispatches PHP scripts to separate FrankenPHP threads for true parallelism. Works with ANY blocking PHP code.

**Level 2 — Coroutines:** Within each thread, Swow `async()`/`await()` fires multiple coroutines for cooperative concurrency. Only works for Swow-hooked I/O (`usleep`, `file_get_contents`, etc). Multiplies concurrency without more threads.

```
index.php (1 main thread)
  +- 10x Script::async("worker.php")        <- 10 FrankenPHP threads (true parallel)
       +- each: 50x Swow coroutines          <- cooperative concurrency within thread
            +- usleep / file_get_contents     <- Swow hooks blocking I/O
```

## How It Works

```
PHP  ->  Script::async()  ->  Go task manager  ->  FrankenPHP threads
                               semaphore queue     parallel execution
     <-  Task::awaitAll()  <-  collect results  <-  JSON responses
```

1. **PHP** splits work into batches and calls `Script::async()` per batch
2. **Go task manager** queues tasks through a semaphore (limits concurrent PHP threads)
3. **FrankenPHP** executes each batch on a separate thread
4. **Swow coroutines** within each thread run items concurrently
5. **PHP** calls `Task::awaitAll()` to collect all results

## FrankenPHP Fork

FrankenAsync requires a [fork of FrankenPHP](https://github.com/nicholasgasior/frankenphp) that adds APIs not available in upstream FrankenPHP. The `Frankenphp\Script` and `Frankenphp\Async\Task` PHP classes are implemented as a C extension that calls back into Go to reach the per-request task manager — upstream FrankenPHP doesn't expose the thread or extension plumbing to make that possible.

The fork adds:

| API | Language | Purpose |
|---|---|---|
| `frankenphp.Thread(index)` | Go | Retrieves a PHP thread by index, returning its `*http.Request` — which carries the request context where the task manager is stored |
| `frankenphp.RegisterExtension(ptr)` | Go | Registers a C `zend_module_entry` as a PHP extension during `init()`, so the `Script` and `Task` classes exist in PHP |
| `frankenphp_thread_index()` | C | Returns the current thread's index from C code, so PHP extension methods can call `Thread(index)` to get back into Go |

The call chain:

```
PHP: (new Script('worker.php'))->async(['batch_ids' => '...'])
  → C:  PHP_METHOD(Script, async)          // phpext.c
  → C:  frankenphp_thread_index()          // gets current thread index
  → Go: go_execute_script_async(index,...) // phpext.go (CGO export)
  → Go: frankenphp.Thread(index)           // retrieves the request context
  → Go: asynctask.FromContext(ctx)         // gets the task manager
  → Go: manager.Async(runnable)            // executes on a new FrankenPHP thread
```

The fork is referenced via a `replace` directive in `go.mod`:

```
replace github.com/dunglas/frankenphp v1.9.0 => ../frankenphp
```

## Quick Start

### Prerequisites

- Go 1.26+
- PHP with Swow extension (built from source with CGO support)
- The [FrankenPHP fork](https://github.com/nicholasgasior/frankenphp) cloned as a sibling directory (`../frankenphp`)

### Setup

Create an `env.yaml` in the project root with your local PHP CGO flags:

```yaml
HOME: "/Users/you"
GOPATH: "/Users/you/go"
GOFLAGS: "-tags=nowatcher"
CGO_ENABLED: "1"
CGO_CFLAGS: "-I/path/to/php/include ..."
CGO_CPPFLAGS: "-I/path/to/php/include ..."
CGO_LDFLAGS: "-L/path/to/php/lib -lphp ..."
```

The CGO flags must point to your PHP build's include headers and libraries. If your PHP build has a Makefile with `cflags`/`ldflags` targets, you can generate the values from there.

### Build & Run

```bash
make build   # Build the binary (dist/frankenasync)
make run     # Build + start the server on :8081
make bench   # Build + run automated test suite
```

Build tag `nowatcher` is required (set via `GOFLAGS` in `env.yaml`).

### GoLand

Configure GoLand to load `env.yaml` as environment variables.

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `FRANKENASYNC_PORT` | `8081` | HTTP listen port |
| `FRANKENASYNC_THREADS` | `66` | FrankenPHP thread pool size (unstable above ~70) |
| `FRANKENASYNC_WORKERS` | `64` | Max concurrent subrequests (capped at threads - 2) |
| `FRANKENASYNC_SWOW` | `1` | `0` = disable Swow (vanilla PHP mode) |

### URL Parameters

| Parameter | Default | Description |
|---|---|---|
| `n` | `100` | Total number of tasks (comment fetches) |
| `threads` | `10` | Number of FrankenPHP threads (batches) |
| `local` | `1` | `1` = simulated I/O (usleep), `0` = real HTTP via local Go API |
| `coroutines` | `1` | `1` = Swow coroutines per thread, `0` = blocking (1 task per thread) |

Examples:
- `?n=100&threads=10` — 100 tasks, 10 threads, ~10 coroutines each (default)
- `?n=100&threads=1` — all coroutines, 1 thread (concurrency only, no parallelism)
- `?n=50&coroutines=0` — 50 threads, 1 task each (parallelism only, no coroutines)

## PHP API

### Script Execution (Level 1 — Threads)

```php
use Frankenphp\Script;
use Frankenphp\Async\Task;

// Fire async subrequests
$task1 = (new Script('api/slow.php'))->async(['id' => 1]);
$task2 = (new Script('api/fast.php'))->async(['id' => 2]);

// Wait for all to complete
$results = Task::awaitAll([$task1, $task2], "5s");

// Sync execution
$result = (new Script('api/hello.php'))->execute();

// Deferred (starts on first await)
$task = (new Script('api/lazy.php'))->defer();
$result = $task->await("5s");
```

### Swow Coroutines (Level 2 — Coroutines)

```php
use function Frankenphp\async;
use function Frankenphp\await;

// Fire coroutines within a single thread
$tasks = [];
$tasks[] = async(fn() => file_get_contents('https://api.example.com/1'));
$tasks[] = async(fn() => file_get_contents('https://api.example.com/2'));
$tasks[] = async(function() {
    usleep(100000); // Swow hooks this — yields to other coroutines
    return 'done';
});

$results = await($tasks, "5s");
```

### Async Utilities

```php
use function Frankenphp\Async\race;
use function Frankenphp\Async\any;
use function Frankenphp\Async\retry;
use function Frankenphp\Async\parallel;
use function Frankenphp\Async\sleep;
use function Frankenphp\Async\timeout;

// Race: first to complete wins
$result = yield from race([fn() => fetch('/fast'), fn() => fetch('/slow')], "5s");

// Any: first successful result (ignores failures)
$result = yield from any([fn() => fetch('/primary'), fn() => fetch('/fallback')]);

// Retry with exponential backoff
$result = yield from retry(3, fn() => fetch('/flaky'), "1s", 2.0);

// Parallel with concurrency limit
$results = yield from parallel($callables, concurrency: 5);
```

### Task Methods

```php
$task->await("5s");           // Wait for completion
$task->cancel();              // Cancel the task
$task->getStatus();           // Status enum
$task->getDuration();         // Execution time in ms
$task->getError();            // Error message if failed

Task::awaitAll($tasks, "30s"); // Wait for all
Task::awaitAny($tasks, "30s"); // Wait for first
```

## Architecture

Concurrency is controlled at three levels:

1. **Worker semaphore** (`FRANKENASYNC_WORKERS`, default 64) — limits concurrent Go goroutines
2. **PHP thread pool** (`FRANKENASYNC_THREADS`, default 66) — fixed pool of FrankenPHP threads
3. **Swow coroutines** — cooperative concurrency within each thread (no thread limit)

Tasks exceeding the semaphore limit queue up and execute as slots become available (sliding window).

## Project Structure

```
frankenasync/
|-- main.go              # HTTP server, FrankenPHP init, request handling
|-- asynctask/           # Go task manager (async, defer, await, cancel)
|   |-- manager.go       # Task lifecycle, semaphore, goroutine pool
|   |-- manager_option.go # Configuration options
|   +-- context.go       # Request context helpers
|-- phpext/              # C + Go PHP extension
|   |-- phpext.go        # Go exports (script exec, task await, etc.)
|   |-- phpext.c         # PHP class registration (Script, Task)
|   |-- phpext.h         # PHP class declarations + arginfos
|   |-- phpext_cgo.h     # CGO bridge header
|   |-- util.c           # Exception helpers
|   +-- util.h           # Exception declarations
|-- examples/            # PHP demo pages
|   |-- index.php        # Main demo (two-level dispatch)
|   |-- lib/
|   |   +-- async.php    # Swow coroutine library (auto-prepended)
|   +-- include/
|       |-- worker.php   # Coroutine-powered batch subrequest
|       +-- task.php     # Single blocking task (pure-thread demo)
|-- bench.sh             # Automated test suite
|-- env.yaml             # IDE environment variables (GoLand)
+-- Makefile             # Build targets
```

## FrankenPHP Thread Limit

FrankenPHP becomes unstable above ~70 threads — requests start failing intermittently after repeated use. This behavior was observed in recent versions of FrankenPHP; earlier versions handled higher thread counts without issues. The default of 66 threads with a 64-worker semaphore is the tested safe maximum. The semaphore is automatically capped at `threads - 2` to prevent overloading.

This is also why the two-level approach matters: instead of pushing threads to 128+ (unstable), 10 threads with Swow coroutines achieves **100x+ speedup** — far beyond what even 128 stable threads could deliver.

## Swow: Vanilla PHP Mode

Swow can be fully disabled with `FRANKENASYNC_SWOW=0`. This sets `swow.enable=Off` in PHP ini, which disables all hooks at the C level before any transports are registered. Only the blocking mode (`coroutines=0`) works when Swow is off.

### Blocking mode: Swow ON vs OFF

| Mode | Swow | Threads | Tasks | Speedup |
|------|------|---------|-------|---------|
| Blocking | ON | 500 | 500 | **56x** |
| Blocking | OFF | 500 | 500 | **56x** |

Identical — Swow is transparent when no coroutines are used.

### Swow Multi-Thread HTTP Contention

When Swow is enabled, it hooks all stream I/O globally. Within a single thread this works perfectly (200+ concurrent HTTP connections), but across multiple FrankenPHP threads Swow's event infrastructure contends. The practical limit is ~100 total concurrent sockets across all threads.

**Impact:** HTTP mode (`local=0`) chunks coroutines into batches of 10 per thread to stay under this limit, yielding ~66x speedup. Local mock mode (`local=1`, using `usleep`) has no such limit and achieves 100x+ speedup.

## License

Code is MIT — see [LICENSE.md](LICENSE.md). The [talk material](talk.md) is licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/) — free to share and adapt with attribution.

## Postcardware

If you use this in a project or adapt the talk material, we'd love a postcard!

**Johan Janssens**
Ganzenbeemd 7
3294 Molenstede
Belgium
