# FAQ

Common questions from the [ConFoo 2026 talk](https://confoo.ca/en/2026/session/php-150x-faster-still-legacy-friendly) and social media.

---

### Why not Symfony Process or AMPHP Parallel?

These aren't child processes. `Script::async()` dispatches internal subrequests to pre-warmed FrankenPHP threads — no `fork()`, no HTTP overhead, ~3-7μs startup per task. The child script runs in a full CGI context on its own thread with true parallelism.

Symfony Process spawns OS processes (`proc_open`). AMPHP Parallel uses ext-parallel to fork workers. Both carry process startup cost and require serializing data across process boundaries. FrankenAsync runs everything inside the same Go process on pre-warmed threads — and your existing blocking PHP code works unchanged, no async rewrites needed.

### "PHP can't spawn PHP" — can't it technically?

PHP can `exec()`, `fork()`, `proc_open()` — but it can't clone the parent CGI context onto a new pre-warmed thread. That's what FrankenPHP provides. The child script runs as if it's a separate HTTP request, on its own thread, inside the same process. It gets its own `$_GET`, `$_POST`, headers, and output buffer — no shared state, no serialization.

The key insight: FrankenPHP's thread pool is managed by Go, not PHP. Go dispatches work to threads that already have PHP initialized and ready. PHP just sees a normal request.

### Why not an event loop (ReactPHP, Revolt, Swoole)?

Event loops multiplex I/O on a single thread. Everything must be non-blocking — one blocking call stalls the entire loop. This means rewriting code to use async-aware libraries, replacing `file_get_contents()` with async HTTP clients, PDO with async database drivers, etc.

FrankenAsync gives each task its own thread with blocking I/O. `file_get_contents()`, PDO, `curl_exec()` — all work as-is. True parallelism without event loop complexity. The trade-off: you use more threads, but threads are cheap when they're pre-warmed and managed by Go's runtime.

### How does this work with frameworks like Symfony or Laravel?

The async scripts run in a standard CGI context — they're regular PHP scripts that receive request parameters and produce output. Framework integration is a separate challenge (bootstrapping the framework in each subrequest adds overhead).

The key point: your existing blocking code works unchanged. The orchestration layer (`Script::async()`, `Future::awaitAll()`, `race()`, `parallel()`) lives in the entry point that dispatches work. The scripts being dispatched don't need to know they're running concurrently.

### What about global state and thread safety?

Each async script runs in its own thread with its own PHP context — separate `$_GET`, `$_POST`, `$_SERVER`, separate output buffer, separate global scope. There's no shared PHP state between threads. It's the same isolation you'd get from separate HTTP requests, just without the HTTP overhead.

This means frameworks that rely on global state (singletons, static properties, service containers) work fine *within* each script — they just can't share that state *across* scripts. That's by design. If you need to pass data between the orchestrator and a task, you do it through request parameters (in) and response output (out).

### What about uncaught PHP exceptions?

An uncaught exception in an async script comes back as a failed Future with a 500 status. You can inspect the error via `$future->getError()`.

Typed exceptions cover task lifecycle errors:
- **FutureTimeoutException** — task exceeded its timeout
- **FutureCanceledException** — task was cancelled (e.g., loser in a `race()`)
- **FutureException** — general task failure

C-level exception interception (catching the original PHP exception type and message across the thread boundary) is a future improvement.
