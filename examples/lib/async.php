<?php

namespace Frankenphp;

use Swow\Coroutine;
use Swow\Channel;
use Swow\ChannelException;

/**
 * Exception thrown when a task exceeds its allowed time.
 */
class TimeoutException extends \Exception {}

/**
 * Executes a callable asynchronously in a coroutine
 *
 * @template T
 * @param callable(): T $callable The callable representing the work to perform asynchronously.
 * @return \Generator<mixed, mixed, mixed, T|\Throwable> A generator that yields once and resolves to either:
 *         - The result of the callable (type T)
 *         - A Throwable if an exception occurred during execution
 * @throws \Exception If the channel operation fails
 */
function async(callable $callable): \Generator
{
    $channel = new Channel(1);

    // Start the coroutine immediately
    Coroutine::run(function () use ($callable, $channel) {
        try {
            $result = $callable();
            $channel->push($result);
        } catch (\Throwable $e) {
            $channel->push($e);
        }
    });

    // Return a generator that yields once and provides the result
    yield $channel->pop();
}

/**
 * Defers execution of a callable until the current scope exits
 *
 * @param callable $callable The cleanup function to defer
 * @return void
 */
function defer(callable $callable): void
{
    \Swow\defer($callable);
}

/**
 * Awaits the completion of one or more concurrent tasks.
 *
 * Usage:
 * - await([$task1, $task2], 5.0) - wait up to 5 seconds
 * - await([$task1, $task2], "5s") - wait up to 5 seconds (duration string)
 *
 * @param array $tasks Tasks to execute concurrently
 * @param float|string $timeout Maximum time to wait
 * @return mixed Single result if one task given, array of results if multiple tasks given
 * @throws \Frankenphp\TimeoutException If the operation exceeds the specified timeout
 * @throws \Throwable Any exception thrown by any of the tasks
 */
function await(array $tasks, float|string $timeout = 0): mixed
{
    if (empty($tasks)) {
        throw new \InvalidArgumentException("await() requires at least one task");
    }

    // Convert timeout to milliseconds
    if (is_string($timeout)) {
        $timeoutMs = (int)(\Frankenphp\Async\duration($timeout) * 1000);
    } else {
        $timeoutMs = (int)($timeout * 1000);
    }

    $count = count($tasks);
    $results = array_fill(0, $count, null);
    $chan = new Channel($count);
    $coroutines = [];

    // Ensure timeout is never negative
    $timeoutMs = max(0, $timeoutMs);

    $getGeneratorResult = function (\Generator $generator) {
        $result = null;

        while ($generator->valid()) {
            $result = $generator->current();
            $generator->next();
        }

        $returnValue = $generator->getReturn();
        if ($returnValue !== null) {
            return $returnValue;
        }

        if ($result instanceof \Throwable) {
            throw $result;
        }

        return $result;
    };

    // Start all coroutines
    foreach ($tasks as $i => $task) {
       $coroutines[$i] = Coroutine::run(function () use ($task, $chan, $i, $timeoutMs, $getGeneratorResult) {
           try {
               $result = null;

               if (is_callable($task)) {

                   $taskResult = $task();

                   if ($taskResult instanceof \Generator) {
                       $result = $getGeneratorResult($taskResult);
                   } else {
                       $result = $taskResult;
                   }

               } elseif ($task instanceof \Generator) {
                   $result = $getGeneratorResult($task);
               } elseif ($task instanceof \Frankenphp\Async\Task) {
                   $result = $task->await($timeoutMs);
               } else {
                   throw new \InvalidArgumentException("Task must be either a callable, a generator, or an AsyncTask object.");
               }

               $chan->push(['index' => $i, 'result' => $result], -1);

           } catch (\Throwable $e) {
                $chan->push(['index' => $i, 'result' => $e], -1);
           }
       });
    }

    $done = 0;
    $endTime = $timeoutMs > 0 ? microtime(true) + ($timeoutMs / 1000) : 0;

    while ($done < $count) {
       try {
           $remainingTimeout = -1;

           if ($timeoutMs > 0) {
               $remainingTimeout = (int)(($endTime - microtime(true)) * 1000);

               if ($remainingTimeout <= 0) {
                   throw new TimeoutException("await() timed out after " . ($timeout ?: "{$timeoutMs}ms"));
               }

               $remainingTimeout = max(1, $remainingTimeout);
           }

           $msg = $chan->pop($remainingTimeout);

           if ($msg['result'] instanceof \Throwable) {
               throw $msg['result'];
           }

           $results[$msg['index']] = $msg['result'];
           $done++;

       } catch (\Throwable $e) {

           if (($e instanceof ChannelException) && str_contains($e->getMessage(), 'reason: Timed out')) {
               $e = new TimeoutException("await() timed out after " . ($timeout ?: "{$timeoutMs}ms"));
           }

            foreach ($coroutines as $coroutine) {
                if ($coroutine->isExecuting()) {
                    $coroutine->kill();
                }
            }

            foreach($tasks as $task) {
                if($task instanceof \Frankenphp\Async\Task) {
                    $task->cancel();
                }
            }

           throw $e;
       }
    }

    return count($tasks) === 1 ? $results[0] : $results;
}

// ============================================================================
// Async Utilities Namespace
// ============================================================================

namespace Frankenphp\Async;

/**
 * Convert a duration to seconds (float)
 * Supports Go-style duration strings: "300ms", "1.5s", "2m", "1h30m", etc.
 *
 * @internal
 * @param float|int|string $duration Duration as number (seconds) or string
 * @return float Duration in seconds
 */
function duration(float|int|string $duration): float
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

    if ($total <= 0) {
        throw new \InvalidArgumentException("Duration must be positive: '$duration'");
    }

    return $total;
}

/**
 * Non-blocking sleep using Swow's hooked sleep functions
 *
 * @param float|string $duration Duration to sleep
 * @return \Generator
 */
function sleep(float|string $duration): \Generator
{
    $seconds = duration($duration);

    yield from \Frankenphp\async(function () use ($seconds) {
        \usleep((int)($seconds * 1_000_000));
        return null;
    });
}

/**
 * Execute a callable with a timeout
 *
 * @param float|string $duration Timeout duration
 * @param callable $callable The operation to execute
 * @return \Generator
 * @throws \Frankenphp\TimeoutException If operation exceeds timeout
 */
function timeout(float|string $duration, callable $callable): \Generator
{
    $seconds = duration($duration);

    $task = \Frankenphp\async($callable);
    $result = \Frankenphp\await([$task], $seconds);
    yield $result;
}

/**
 * Race multiple tasks and return the first one to complete
 *
 * @param array $tasks Array of callables or generators to race
 * @param float|string $timeout Optional timeout
 * @return \Generator Yields the result of the first completed task
 * @throws \Frankenphp\TimeoutException If timeout is exceeded
 */
function race(array $tasks, float|string $timeout = 0): \Generator
{
    if (empty($tasks)) {
        throw new \InvalidArgumentException("race() requires at least one task");
    }

    $timeoutSeconds = $timeout ? duration($timeout) : 0;

    $generators = [];
    foreach ($tasks as $task) {
        if (is_callable($task)) {
            $generators[] = \Frankenphp\async($task);
        } elseif ($task instanceof \Generator) {
            $generators[] = $task;
        } else {
            throw new \InvalidArgumentException("Each task must be callable or a generator");
        }
    }

    $channel = new \Swow\Channel(1);
    $coroutines = [];
    $completed = false;

    foreach ($generators as $i => $generator) {
        $coroutines[$i] = \Swow\Coroutine::run(function () use ($generator, $channel, &$completed, $i) {
            try {
                $result = null;
                while ($generator->valid()) {
                    $result = $generator->current();
                    $generator->next();
                }

                $returnValue = $generator->getReturn();
                if ($returnValue !== null) {
                    $result = $returnValue;
                }

                if (!$completed) {
                    $completed = true;
                    $channel->push(['index' => $i, 'result' => $result], -1);
                }
            } catch (\Throwable $e) {
                if (!$completed) {
                    $completed = true;
                    $channel->push(['index' => $i, 'error' => $e], -1);
                }
            }
        });
    }

    try {
        $timeoutMs = $timeoutSeconds > 0 ? (int)($timeoutSeconds * 1000) : -1;
        $winner = $channel->pop($timeoutMs);

        foreach ($coroutines as $coroutine) {
            if ($coroutine->isExecuting()) {
                $coroutine->kill();
            }
        }

        if (isset($winner['error'])) {
            throw $winner['error'];
        }

        yield $winner['result'];

    } catch (\Swow\ChannelException $e) {
        foreach ($coroutines as $coroutine) {
            if ($coroutine->isExecuting()) {
                $coroutine->kill();
            }
        }

        if (str_contains($e->getMessage(), 'Timed out')) {
            throw new \Frankenphp\TimeoutException("race() timed out after " . ($timeout ?: $timeoutSeconds . "s"));
        }
        throw $e;
    }
}

/**
 * Wait for the first successful task, ignoring failures
 *
 * @param array $tasks Array of callables or generators
 * @param float|string $timeout Optional timeout
 * @return \Generator Yields the result of the first successful task
 * @throws \Frankenphp\TimeoutException If timeout is exceeded
 */
function any(array $tasks, float|string $timeout = 0): \Generator
{
    if (empty($tasks)) {
        throw new \InvalidArgumentException("any() requires at least one task");
    }

    $timeoutSeconds = $timeout ? duration($timeout) : 0;

    $generators = [];
    foreach ($tasks as $task) {
        if (is_callable($task)) {
            $generators[] = \Frankenphp\async($task);
        } elseif ($task instanceof \Generator) {
            $generators[] = $task;
        } else {
            throw new \InvalidArgumentException("Each task must be callable or a generator");
        }
    }

    $channel = new \Swow\Channel(count($generators));
    $coroutines = [];

    foreach ($generators as $i => $generator) {
        $coroutines[$i] = \Swow\Coroutine::run(function () use ($generator, $channel, $i) {
            try {
                $result = null;
                while ($generator->valid()) {
                    $result = $generator->current();
                    $generator->next();
                }

                $returnValue = $generator->getReturn();
                if ($returnValue !== null) {
                    $result = $returnValue;
                }

                $channel->push(['index' => $i, 'success' => true, 'result' => $result], -1);
            } catch (\Throwable $e) {
                $channel->push(['index' => $i, 'success' => false, 'error' => $e], -1);
            }
        });
    }

    $startTime = microtime(true);
    $lastException = null;

    try {
        for ($i = 0; $i < count($generators); $i++) {
            $remainingTimeout = -1;
            if ($timeoutSeconds > 0) {
                $elapsed = microtime(true) - $startTime;
                $remainingTimeout = max(1, (int)(($timeoutSeconds - $elapsed) * 1000));

                if ($remainingTimeout <= 0) {
                    throw new \Frankenphp\TimeoutException("any() timed out after " . ($timeout ?: $timeoutSeconds . "s"));
                }
            }

            $taskResult = $channel->pop($remainingTimeout);

            if ($taskResult['success']) {
                foreach ($coroutines as $coroutine) {
                    if ($coroutine->isExecuting()) {
                        $coroutine->kill();
                    }
                }

                yield $taskResult['result'];
                return;
            }

            $lastException = $taskResult['error'];
        }

        throw $lastException;

    } catch (\Swow\ChannelException $e) {
        foreach ($coroutines as $coroutine) {
            if ($coroutine->isExecuting()) {
                $coroutine->kill();
            }
        }

        if (str_contains($e->getMessage(), 'Timed out')) {
            throw new \Frankenphp\TimeoutException("any() timed out after " . ($timeout ?: $timeoutSeconds . "s"));
        }
        throw $e;
    }
}

/**
 * Retry a callable with exponential backoff
 *
 * @param int $attempts Maximum number of attempts
 * @param callable $callable The operation to retry
 * @param float|string $delay Initial delay between retries
 * @param float $multiplier Backoff multiplier (default: 2.0)
 * @return \Generator Yields the successful result
 * @throws \Throwable The last exception if all attempts fail
 */
function retry(int $attempts, callable $callable, float|string $delay = 1.0, float $multiplier = 2.0): \Generator
{
    if ($attempts < 1) {
        throw new \InvalidArgumentException("Attempts must be at least 1");
    }

    $delaySeconds = duration($delay);
    $lastException = null;

    for ($attempt = 1; $attempt <= $attempts; $attempt++) {
        try {
            $result = \Frankenphp\await([\Frankenphp\async($callable)]);
            yield $result;
            return;
        } catch (\Throwable $e) {
            $lastException = $e;

            if ($attempt < $attempts && $delaySeconds > 0) {
                \usleep((int)($delaySeconds * 1_000_000));

                if ($multiplier > 1.0) {
                    $delaySeconds *= $multiplier;
                }
            }
        }
    }

    throw $lastException;
}

/**
 * Execute tasks in parallel with concurrency limit
 *
 * @param array $tasks Array of callables to execute
 * @param int $concurrency Maximum number of tasks to run simultaneously
 * @return \Generator Yields array of results in same order as input tasks
 */
function parallel(array $tasks, int $concurrency = 10): \Generator
{
    if (empty($tasks)) {
        yield [];
        return;
    }

    if ($concurrency < 1) {
        throw new \InvalidArgumentException("Concurrency must be at least 1");
    }

    $taskCount = count($tasks);
    $results = array_fill(0, $taskCount, null);
    $channel = new \Swow\Channel($taskCount);
    $running = 0;
    $started = 0;
    $completed = 0;

    foreach ($tasks as $index => $task) {
        if (!is_callable($task)) {
            throw new \InvalidArgumentException("All tasks must be callable");
        }

        if ($running >= $concurrency) {
            break;
        }

        \Swow\Coroutine::run(function () use ($task, $channel, $index) {
            try {
                $result = $task();
                $channel->push(['index' => $index, 'result' => $result, 'error' => null], -1);
            } catch (\Throwable $e) {
                $channel->push(['index' => $index, 'result' => null, 'error' => $e], -1);
            }
        });

        $running++;
        $started++;
    }

    while ($completed < $taskCount) {
        $msg = $channel->pop();

        if ($msg['error'] !== null) {
            throw $msg['error'];
        }

        $results[$msg['index']] = $msg['result'];
        $running--;
        $completed++;

        if ($started < $taskCount) {
            $index = $started;
            $task = $tasks[$index];

            \Swow\Coroutine::run(function () use ($task, $channel, $index) {
                try {
                    $result = $task();
                    $channel->push(['index' => $index, 'result' => $result, 'error' => null], -1);
                } catch (\Throwable $e) {
                    $channel->push(['index' => $index, 'result' => null, 'error' => $e], -1);
                }
            });

            $running++;
            $started++;
        }
    }

    yield $results;
}

/**
 * Throttle a callable to execute at most once per interval
 *
 * @param callable $callable The operation to throttle
 * @param float|string $interval Minimum time between executions
 * @return \Closure Throttled version of the callable
 */
function throttle(callable $callable, float|string $interval): \Closure
{
    $intervalSeconds = duration($interval);
    $lastExecution = 0.0;

    return function (...$args) use ($callable, $intervalSeconds, &$lastExecution): \Generator {
        $now = microtime(true);
        $timeSinceLastExecution = $now - $lastExecution;

        if ($timeSinceLastExecution < $intervalSeconds) {
            $waitTime = $intervalSeconds - $timeSinceLastExecution;
            yield from sleep($waitTime);
        }

        $lastExecution = microtime(true);

        $result = $callable(...$args);

        if ($result instanceof \Generator) {
            return yield from $result;
        }

        return yield $result;
    };
}
