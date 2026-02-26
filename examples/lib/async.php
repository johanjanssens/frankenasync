<?php
/**
 * Structured concurrency helpers for FrankenAsync.
 *
 * Composable generators on top of Script::async() and Future.
 * No Swow, no coroutines — just plain PHP generators and threads.
 */

namespace Frankenphp\Async;

use Frankenphp\Script;
use Frankenphp\Async\Future;

/**
 * Race multiple futures — first wins, rest get cancelled.
 *
 * Usage:
 *   $result = yield from race([
 *       (new Script('primary.php'))->async(),
 *       (new Script('fallback.php'))->async(),
 *   ], "5s");
 *
 * @param Future[] $tasks Array of Future objects (already dispatched)
 * @param string $timeout Go-style duration string ("5s", "500ms")
 * @return \Generator Yields the result of the first completed task
 */
function race(array $tasks, string $timeout = "30s"): \Generator
{
    $result = Future::awaitAny($tasks, $timeout);

    // Cancel remaining tasks
    foreach ($tasks as $task) {
        if ($task->getStatus() === 'running') {
            $task->cancel();
        }
    }

    yield $result;
}

/**
 * Retry a callable with exponential backoff.
 *
 * The callable must return a Future (via Script::async()).
 *
 * Usage:
 *   $result = yield from retry(3,
 *       fn() => (new Script('flaky.php'))->async(['id' => 1]),
 *       "1s", 2.0);
 *
 * @param int $attempts Maximum number of attempts
 * @param callable(): Future $callable Returns a Future to await
 * @param string $delay Initial delay between retries ("100ms", "1s")
 * @param float $multiplier Backoff multiplier (default: 2.0)
 * @return \Generator Yields the successful result
 * @throws \RuntimeException If all attempts fail
 */
function retry(
    int $attempts,
    callable $callable,
    string $delay = "100ms",
    float $multiplier = 2.0,
): \Generator {
    $delayMs = (int)(duration($delay) * 1000);
    $lastError = null;

    for ($attempt = 1; $attempt <= $attempts; $attempt++) {
        $task = $callable();

        try {
            $result = $task->await("30s");

            if ($task->getError() === null) {
                yield $result;
                return;
            }

            $lastError = $task->getError();
        } catch (\Throwable $e) {
            $lastError = $e->getMessage();
        }

        if ($attempt < $attempts && $delayMs > 0) {
            usleep($delayMs * 1000);
            $delayMs = (int)($delayMs * $multiplier);
        }
    }

    throw new \RuntimeException("Failed after $attempts attempts: $lastError");
}

/**
 * Execute callables in parallel with a concurrency limit.
 *
 * Each callable must return a Future (via Script::async()).
 * Results are returned in the same order as input.
 *
 * Usage:
 *   $results = yield from parallel($callables, concurrency: 5);
 *
 * @param array<callable(): Future> $callables Array of callables returning Futures
 * @param int $concurrency Max concurrent tasks (sliding window)
 * @param string $timeout Timeout per batch
 * @return \Generator Yields array of results in input order
 */
function parallel(
    array $callables,
    int $concurrency = 10,
    string $timeout = "30s",
): \Generator {
    if (empty($callables)) {
        yield [];
        return;
    }

    $results = [];
    foreach (array_chunk($callables, $concurrency, true) as $chunk) {
        $tasks = [];
        foreach ($chunk as $index => $callable) {
            $tasks[$index] = $callable();
        }

        $batchResults = Future::awaitAll(array_values($tasks), $timeout);

        $i = 0;
        foreach ($tasks as $index => $task) {
            $results[$index] = $batchResults[$i++];
        }
    }

    yield $results;
}

/**
 * Throttle tasks into batches — yields results as each batch completes.
 *
 * Generator-based: streams results instead of collecting everything into memory.
 *
 * Usage:
 *   foreach (throttle($ids, 'task.php', batch: 50) as $result) {
 *       process($result);
 *   }
 *
 * @param array $ids Array of IDs to process
 * @param string $script PHP script to execute for each ID
 * @param array $params Base parameters (id is added automatically)
 * @param int $batch Batch size (tasks per round)
 * @param string $timeout Timeout per batch
 * @return \Generator Yields individual results as batches complete
 */
function throttle(
    array $ids,
    string $script,
    array $params = [],
    int $batch = 10,
    string $timeout = "30s",
): \Generator {
    foreach (array_chunk($ids, $batch) as $chunk) {
        $tasks = [];
        foreach ($chunk as $id) {
            $tasks[] = (new Script($script))->async(array_merge($params, ['id' => $id]));
        }

        $results = Future::awaitAll($tasks, $timeout);

        foreach ($results as $result) {
            yield $result;
        }
    }
}

/**
 * Parse a Go-style duration string to seconds.
 *
 * @internal
 * @param string $duration Duration string ("100ms", "1s", "5m", "1h30m")
 * @return float Duration in seconds
 */
function duration(string $duration): float
{
    if (is_numeric($duration)) {
        return (float) $duration;
    }

    $duration = trim($duration);
    $total = 0.0;

    if (!preg_match_all('/([0-9]*\.?[0-9]+)(ns|us|µs|ms|s|m|h)/i', $duration, $matches, PREG_SET_ORDER)) {
        throw new \InvalidArgumentException("Invalid duration format: '$duration'");
    }

    foreach ($matches as $match) {
        $value = (float) $match[1];
        $unit = strtolower($match[2]);

        $total += match ($unit) {
            'ns' => $value / 1_000_000_000,
            'us', 'µs' => $value / 1_000_000,
            'ms' => $value / 1_000,
            's' => $value,
            'm' => $value * 60,
            'h' => $value * 3600,
            default => throw new \InvalidArgumentException("Unknown duration unit: '$unit'"),
        };
    }

    return $total;
}
