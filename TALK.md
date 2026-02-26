# Concurrent PHP with FrankenPHP Threads

> Base narrative for FrankenPHP conference talks

## Abstract

Your PHP scripts are constantly waiting for the database, HTTP requests, or files. Solutions exist,
but often require rewriting code.

In this talk, I will show you how to push PHP to its limits running scripts 150x+ faster. The
ingredients: FrankenPHP threads, a Go semaphore sliding window, a sprinkle of PHP, and a dash
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

What if you could keep your existing PHP code and still get 150x+ speedup?

## The Ingredients

- **FrankenPHP threads** — true parallelism for any PHP code, including blocking I/O
- **Go semaphore** — sliding window that queues tasks beyond the worker limit
- **A dash of Go** — orchestrates the thread pool, semaphore queue, and task lifecycle

## The Solution: Thread-Level Parallelism

FrankenPHP threads provide true parallelism for any PHP code. Each `Script::async()` call dispatches
a PHP script to a separate thread. A Go semaphore controls how many run simultaneously — tasks
beyond the limit queue and execute as slots free up (sliding window).

```
index.php
  +- Script::async("task.php", ['id' => 1])   <- FrankenPHP thread
  +- Script::async("task.php", ['id' => 2])   <- FrankenPHP thread
  +- Script::async("task.php", ['id' => 3])   <- FrankenPHP thread
  ...
  +- Future::awaitAll($tasks, "60s")           <- collect results
```

Any PHP code works — C extensions, legacy libraries, blocking I/O. No code changes required.
The Go semaphore handles the concurrency transparently.

## The Numbers

Wall clock stays flat at ~0.5s regardless of task count. Speedup scales linearly.

### Thread Parallelism with Go Semaphore

| Tasks | Wall  | Speedup  |
|-------|-------|----------|
| 10    | 0.5s  | 6x       |
| 50    | 0.5s  | 29x      |
| 100   | 0.5s  | 57x      |
| 250   | 0.5s  | 148x     |
| 500   | 0.5s  | 277x     |

The Go semaphore sliding window means you can dispatch far more tasks than threads — they queue
up and execute as slots free up. 500 tasks with 64 concurrent slots still completes in ~0.5s.

### With Real HTTP (local API endpoint)

| Tasks | Wall  | Speedup |
|-------|-------|---------|
| 100   | 0.2s  | 57x     |
| 500   | 0.8s  | 66x     |

Real `file_get_contents` HTTP calls, no code changes.

## What the PHP Developer Writes

```php
use Frankenphp\Script;
use Frankenphp\Async\Future;

$tasks = [];
foreach ($ids as $id) {
    $tasks[] = (new Script('task.php'))->async(['id' => $id]);
}

$results = Future::awaitAll($tasks, "30s");
```

Any PHP script. Any blocking code. Each runs on its own thread. The Go semaphore handles
queuing transparently.

## Structured Concurrency Helpers

Composable generators on top of `Script::async()` and `Future` — no coroutines, no event loop:

```php
use function Frankenphp\Async\{race, retry, parallel, throttle};

// Race: first wins, losers get cancelled
$result = yield from race([
    (new Script('primary.php'))->async(),
    (new Script('fallback.php'))->async(),
], "5s");

// Retry with exponential backoff
$result = yield from retry(3, fn() => (new Script('flaky.php'))->async(), "1s", 2.0);

// Parallel with sliding window concurrency limit
$results = yield from parallel($callables, concurrency: 5);

// Throttle — stream results in batches (generator)
foreach (throttle($ids, 'task.php', batch: 50) as $result) {
    // process each result as batches complete
}
```

Generators are the perfect fit — `throttle` streams results batch by batch instead of collecting
everything into memory. `yield from` lets you compose helpers together. No event loop, no
framework — just PHP.

## Composition — Orchestrate, Don't Rewrite

Your existing PHP scripts — blocking DB queries, API calls, file I/O — stay exactly as they are.
You add a thin orchestration layer on top:

```php
// product.php, reviews.php, stock.php — existing scripts, unchanged
function productPage(int $id): \Generator {
    [$product, $reviews, $stock] = yield from parallel([
        fn() => (new Script('product.php'))->async(['id' => $id]),
        fn() => (new Script('reviews.php'))->async(['id' => $id]),
        fn() => (new Script('stock.php'))->async(['id' => $id]),
    ], concurrency: 3);

    yield compact('product', 'reviews', 'stock');
}

// cart.php, stripe.php, paypal.php — existing scripts, unchanged
function checkout(int $cartId): \Generator {
    $cart = yield from retry(3,
        fn() => (new Script('cart.php'))->async(['id' => $cartId]));

    $payment = yield from race([
        (new Script('stripe.php'))->async($cart),
        (new Script('paypal.php'))->async($cart),
    ], "10s");

    yield $payment;
}
```

The scripts are legacy. The orchestration is new. You don't rewrite your PHP — you compose it.

## Key Takeaway

You don't have to rewrite your PHP code to get massive speedups. FrankenPHP threads handle anything
that blocks, and a thin Go layer with a semaphore sliding window orchestrates it all. Normal PHP
code, legacy-friendly, 150x+ speedup.

---

This work is licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).
You are free to share and adapt this material with appropriate attribution.
