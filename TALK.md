# Two-Level Concurrency in PHP

> Base narrative for FrankenPHP conference talks

## Abstract

Your PHP scripts are constantly waiting for the database, HTTP requests, or files. Solutions exist,
but often require rewriting code.

In this talk, I will show you how to push PHP to its limits running scripts 100x faster. The
ingredients: FrankenPHP threads, Swow coroutines, PHP Generators, a sprinkle of PHP, and a dash
of Go to orchestrate it all. This approach works for any PHP code both legacy and new, including
blocking I/O.

## The Problem

PHP is synchronous. Every `file_get_contents()`, every database query, every `usleep()` blocks the
entire thread. Your script spends most of its time waiting.

Solutions exist — ReactPHP, Swoole, AMPHP — but they all require a long-running PHP CLI process
and rewriting your code around event loops, promises, or async/await patterns. The developer must
understand scheduling, propagate async through the call stack, rewrite libraries to be non-blocking,
and debug temporal bugs instead of logical ones.

This is sold as "control," but it puts the complexity in the wrong place. The runtime stays simple
while application code becomes complex — and that is backwards. Concurrency is infrastructure,
not application logic. The runtime should absorb that complexity so developers don't have to.

Legacy code, blocking libraries, C extensions — none of it fits the async model without
significant rewrites. The question isn't "how do we expose concurrency to developers?" It's
"how do we hide concurrency from developers?"

What if you could keep your existing PHP code and still get 100x+ speedup?

## The Ingredients

- **FrankenPHP threads** — true parallelism for any PHP code, including blocking I/O
- **Swow coroutines** — cooperative concurrency within each thread, hooks standard PHP I/O
- **PHP Generators** — the `yield from` pattern for composable async utilities (`race`, `retry`, `parallel`)
- **A dash of Go** — orchestrates the thread pool, semaphore queue, and task lifecycle

## The Solution: Two Levels

FrankenPHP threads and Swow coroutines work together — each handling what the other can't.

```
index.php
  +- Script::async("worker.php")           <- FrankenPHP thread (Level 1)
       +- async(fn() => usleep(...))        <- Swow coroutine (Level 2)
       +- async(fn() => file_get_contents)  <- Swow coroutine (Level 2)
       +- async(fn() => blocking_c_call())  <- blocks this thread, still works
```

**Level 1 — FrankenPHP Threads.** `Script::async()` dispatches a PHP script to a separate thread.
True parallelism. Works with ANY blocking PHP code — C extensions, legacy libraries, anything.
No code changes required.

**Level 2 — Swow Coroutines.** Within each thread, `async()`/`await()` fires coroutines that
share the thread cooperatively. Swow hooks standard PHP I/O (`usleep`, `file_get_contents`,
`stream_socket_client`, etc.) so they yield instead of block. Multiplies concurrency without
adding threads.

## Why Both?

Neither level is complete on its own:

- **Threads alone** scale linearly but consume resources. 100 tasks = 100 threads.
- **Coroutines alone** only work with Swow-hooked I/O. A blocking C extension call stalls
  all coroutines in that thread.
- **Together:** threads handle what coroutines can't (blocking C code), coroutines handle what
  doesn't need a whole thread (I/O). You mix blocking and non-blocking code freely in a single
  thread — Swow handles part of it, FrankenPHP handles the rest.

The result comes back at the call point. The developer doesn't care which level handled what.

## The Numbers

Wall clock stays flat at ~0.5s regardless of task count. Speedup scales linearly.

### Blocking I/O (threads only, no coroutines)

| Threads | Tasks | Wall  | Speedup |
|---------|-------|-------|---------|
| 10      | 10    | 0.5s  | 6x      |
| 25      | 25    | 0.5s  | 14x     |
| 50      | 50    | 0.5s  | 29x     |

Threads top out — FrankenPHP becomes unstable above ~70 threads. The safe maximum is 66 threads
with a 64-worker semaphore, capping pure-thread speedup around 50x.

### With Coroutines (threads + Swow)

| Threads | Coroutines | Tasks | Wall  | Speedup  |
|---------|-----------|-------|-------|----------|
| 10      | ~10       | 100   | 0.5s  | **57x**  |
| 10      | ~25       | 250   | 0.5s  | **148x** |
| 10      | ~50       | 500   | 0.5s  | **277x** |
| 25      | ~25       | 625   | 0.5s  | **343x** |
| 50      | ~50       | 2500  | 0.5s  | **1306x**|

10 threads with coroutines beats 50 pure threads by 10x. Same PHP code.

### With Real HTTP (local API endpoint)

| Threads | Coroutines | Tasks | Wall  | Speedup |
|---------|-----------|-------|-------|---------|
| 10      | ~10       | 100   | 0.2s  | **57x** |
| 10      | ~50       | 500   | 0.8s  | **66x** |

HTTP mode is chunked (10 concurrent per thread) due to Swow's multi-thread socket contention.
Within a single thread Swow handles 200+ concurrent connections fine, but across multiple OS
threads the practical limit is ~100 total concurrent sockets. Still: 66x with real
`file_get_contents` HTTP calls, no code changes.

### Swow ON vs OFF (vanilla PHP proof)

| Mode | Swow | Threads | Tasks | Speedup |
|------|------|---------|-------|---------|
| Blocking | ON | 500 | 500 | **56x** |
| Blocking | OFF | 500 | 500 | **56x** |

Set `FRANKENASYNC_SWOW=0` to disable Swow entirely (`swow.enable=Off`). Blocking mode
performs identically — Swow is fully transparent when you're not using coroutines. This
proves the thread-level concurrency works with completely vanilla PHP.

## What the PHP Developer Writes

### Level 1 — Thread dispatch

```php
use Frankenphp\Script;
use Frankenphp\Async\Task;

$task1 = (new Script('api/slow.php'))->async(['id' => 1]);
$task2 = (new Script('api/fast.php'))->async(['id' => 2]);

$results = Task::awaitAll([$task1, $task2], "5s");
```

Any PHP script. Any blocking code. Each runs on its own thread.

### Level 2 — Coroutines within a thread

```php
use function Frankenphp\async;
use function Frankenphp\await;

$tasks = [];
$tasks[] = async(fn() => file_get_contents('https://api.example.com/1'));
$tasks[] = async(fn() => file_get_contents('https://api.example.com/2'));
$tasks[] = async(function() {
    usleep(100000);  // Swow hooks this - yields to other coroutines
    return 'done';
});

$results = await($tasks, "5s");
```

Standard PHP functions. Swow hooks them transparently.

### Generator-based utilities

```php
use function Frankenphp\Async\race;
use function Frankenphp\Async\retry;
use function Frankenphp\Async\parallel;

// Race: first to complete wins
$result = yield from race([fn() => fetch('/fast'), fn() => fetch('/slow')], "5s");

// Retry with exponential backoff
$result = yield from retry(3, fn() => fetch('/flaky'), "1s", 2.0);

// Parallel with concurrency limit
$results = yield from parallel($callables, concurrency: 5);
```

PHP Generators (`yield from`) make these composable — no promises, no callbacks.

### Two levels combined

```php
// Split 500 tasks across 10 threads, ~50 coroutines each
$batches = array_chunk(range(1, 500), 50);

$tasks = [];
foreach ($batches as $batch) {
    // Level 1: each batch gets its own thread
    $tasks[] = (new Script('worker.php'))->async([
        'batch_ids' => json_encode($batch),
    ]);
}

// worker.php internally runs Level 2 coroutines per batch item
$results = Task::awaitAll($tasks, "30s");
```

10 threads. 500 tasks. 277x speedup. The async wrapper hides all the complexity.

## Key Takeaway

You don't have to choose between threads and coroutines. FrankenPHP threads handle anything that
blocks, Swow coroutines multiply concurrency for hookable I/O, PHP Generators make the async
utilities composable, and a thin Go layer orchestrates it all. Mix and match in the same request,
collect results at the call point. Normal PHP code, legacy-friendly, 100x+ speedup.

---

This work is licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).
You are free to share and adapt this material with appropriate attribution.
