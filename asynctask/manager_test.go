package asynctask

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test helper functions
func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expected error) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("expected error %v, got %v", expected, err)
	}
}

func assertEqual(t *testing.T, got, want interface{}) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Test Defer basic functionality and idempotency
func TestDefer(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	execCount := int32(0)
	taskID := tm.Defer(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		atomic.AddInt32(&execCount, 1)
		return "deferred result", nil
	}))

	// Task should not have executed yet
	if atomic.LoadInt32(&execCount) != 0 {
		t.Fatal("deferred task executed before await")
	}

	// Check status is deferred
	status, err := tm.Status(taskID)
	assertNoError(t, err)
	assertEqual(t, status, StatusDeferred)

	// Await multiple times - should only execute once
	result1, err1 := tm.Await(ctx, taskID)
	assertNoError(t, err1)
	assertEqual(t, result1.Result, "deferred result")

	result2, err2 := tm.Await(ctx, taskID)
	assertNoError(t, err2)
	assertEqual(t, result2.Result, "deferred result")

	// Should only execute once
	if atomic.LoadInt32(&execCount) != 1 {
		t.Fatalf("expected task to execute once, got %d executions", execCount)
	}

	// Results should be identical
	assertEqual(t, result1.Result, result2.Result)

	// Status should be completed (result1 already completed, so safe to check)
	status, err = tm.Status(taskID)
	assertNoError(t, err)
	assertEqual(t, status, StatusCompleted)
}

// Test WithRetry wrapper with both Async and Defer
func TestWithRetry(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	t.Run("with Async", func(t *testing.T) {
		attempts := int32(0)
		wrapped := WithRetry(RunnableFunc(func(ctx context.Context) (any, error) {
			current := atomic.AddInt32(&attempts, 1)
			if current < 3 {
				return nil, errors.New("temporary error")
			}
			return "success", nil
		}), 3, 10*time.Millisecond)

		taskID := tm.Async(ctx, wrapped)
		result, err := tm.Await(ctx, taskID)
		assertNoError(t, err)
		assertEqual(t, result.Result, "success")
		assertEqual(t, atomic.LoadInt32(&attempts), int32(3))
	})

	t.Run("with Defer", func(t *testing.T) {
		attempts := int32(0)
		wrapped := WithRetry(RunnableFunc(func(ctx context.Context) (any, error) {
			current := atomic.AddInt32(&attempts, 1)
			if current < 2 {
				return nil, errors.New("temporary error")
			}
			return "deferred success", nil
		}), 3, 10*time.Millisecond)

		taskID := tm.Defer(ctx, wrapped)

		// Should not have executed yet
		if atomic.LoadInt32(&attempts) != 0 {
			t.Fatal("deferred task with retry executed before await")
		}

		result, err := tm.Await(ctx, taskID)
		assertNoError(t, err)
		assertEqual(t, result.Result, "deferred success")
	})
}

// Test WithTimeout wrapper with both Async and Defer
func TestWithTimeout(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		wrapped := WithTimeout(RunnableFunc(func(ctx context.Context) (any, error) {
			time.Sleep(10 * time.Millisecond)
			return "completed", nil
		}), 100*time.Millisecond)

		taskID := tm.Async(ctx, wrapped)
		result, err := tm.Await(ctx, taskID)
		assertNoError(t, err)
		assertEqual(t, result.Result, "completed")
	})

	t.Run("timeout exceeded", func(t *testing.T) {
		wrapped := WithTimeout(RunnableFunc(func(ctx context.Context) (any, error) {
			select {
			case <-time.After(200 * time.Millisecond):
				return "should not complete", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}), 50*time.Millisecond)

		taskID := tm.Async(ctx, wrapped)
		_, err := tm.Await(ctx, taskID)
		if !errors.Is(err, ErrTaskFailed) {
			t.Fatalf("expected timeout error, got %v", err)
		}
	})

	t.Run("with Defer", func(t *testing.T) {
		executed := false
		wrapped := WithTimeout(RunnableFunc(func(ctx context.Context) (any, error) {
			executed = true
			return "deferred timeout result", nil
		}), 100*time.Millisecond)

		taskID := tm.Defer(ctx, wrapped)
		if executed {
			t.Fatal("deferred task executed before await")
		}

		result, err := tm.Await(ctx, taskID)
		assertNoError(t, err)
		assertEqual(t, result.Result, "deferred timeout result")
	})
}

// Test composition of wrappers
func TestComposition_TimeoutAndRetry(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	attempts := int32(0)
	wrapped := WithTimeout(
		WithRetry(RunnableFunc(func(ctx context.Context) (any, error) {
			current := atomic.AddInt32(&attempts, 1)
			if current < 2 {
				return nil, errors.New("retry me")
			}
			return "composed result", nil
		}), 3, 10*time.Millisecond),
		500*time.Millisecond,
	)

	taskID := tm.Async(ctx, wrapped)
	result, err := tm.Await(ctx, taskID)
	assertNoError(t, err)
	assertEqual(t, result.Result, "composed result")
}

// Test basic async execution
func TestAsync(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	expected := "test result"
	taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		return expected, nil
	}))

	result, err := tm.Await(ctx, taskID)
	assertNoError(t, err)
	assertEqual(t, result.Result, expected)
	assertEqual(t, result.Error, nil)
}

// Test async with error
func TestAsync_WithError(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	expectedErr := errors.New("test error")
	taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		return nil, expectedErr
	}))

	result, err := tm.Await(ctx, taskID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Check that the error wraps ErrTaskFailed
	if !errors.Is(err, ErrTaskFailed) {
		t.Fatalf("expected error to wrap ErrTaskFailed, got %v", err)
	}

	// Check the actual error in the task
	if !errors.Is(result.Error, expectedErr) {
		t.Fatalf("expected task error %v, got %v", expectedErr, result.Error)
	}
}

// Test task cancellation
func TestTask_Cancellation(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		select {
		case <-time.After(200 * time.Millisecond):
			return "should not complete", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))

	// Cancel the task immediately
	canceled := tm.Cancel(taskID)
	if !canceled {
		t.Fatal("expected task to be canceled")
	}

	// Try to await the canceled task
	_, err := tm.Await(ctx, taskID)
	if err == nil || !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound after cancel, got %v", err)
	}
}

// TestAwait_Cancellation verifies that awaiting a task respects the context cancellation.
func TestAwait_Cancellation(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		time.Sleep(200 * time.Millisecond)
		return "result", nil
	}))

	// Create a context that will be canceled
	awaitCtx, cancel := context.WithCancel(ctx)

	// Cancel the context after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := tm.Await(awaitCtx, taskID)
	if err == nil {
		t.Fatal("expected cancellation error, got nil")
	}

	if !errors.Is(err, ErrTaskCanceled) {
		t.Fatalf("expected ErrTaskCanceled, got %v", err)
	}
}

// TestAwait_Concurrent verifies that multiple goroutines can concurrently
// await the same task without causing race conditions or inconsistent results.
func TestAwait_Concurrent(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		time.Sleep(50 * time.Millisecond)
		return "result", nil
	}))

	const numAwaits = 10
	var wg sync.WaitGroup
	wg.Add(numAwaits)

	results := make([]any, numAwaits)
	errs := make([]error, numAwaits)

	for i := 0; i < numAwaits; i++ {
		go func(idx int) {
			defer wg.Done()
			res, err := tm.Await(ctx, taskID)
			results[idx] = res.Result
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	for i := 0; i < numAwaits; i++ {
		if errs[i] != nil {
			t.Fatalf("Await #%d failed: %v", i, errs[i])
		}
		if results[i] != "result" {
			t.Fatalf("Await #%d got wrong result: %v", i, results[i])
		}
	}
}

// TestAwait_Circular verifies circular await detection.
func TestAwait_Circular(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	var taskA, taskB ID
	ready := make(chan struct{})

	taskA = tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		<-ready // wait until taskB is set
		res, err := tm.Await(ctx, taskB)
		if err != nil {
			return nil, fmt.Errorf("taskA failed: %w", err)
		}
		return "A got " + res.Result.(string), nil
	}))

	taskB = tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		<-ready // wait until taskA is set
		res, err := tm.Await(ctx, taskA)
		if err != nil {
			return nil, fmt.Errorf("taskB failed: %w", err)
		}
		return "B got " + res.Result.(string), nil
	}))

	// Now both taskA and taskB IDs are set
	close(ready)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := tm.Await(ctx, taskA)
		if err != nil {
			t.Logf("Caught error: %v", err)
		}
	}()

	select {
	case <-done:
		t.Fatal("Circular await unexpectedly completed")
	case <-time.After(100 * time.Millisecond):
		t.Log("Circular await detected (test passes)")
	}
}

// TestAwaitAll verifies that AwaitAll waits for all tasks to complete successfully.
func TestAwaitAll(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	var taskIDs []ID
	expectedResults := []string{"result1", "result2", "result3"}

	for i, expected := range expectedResults {
		result := expected // capture loop variable
		delay := time.Duration(i*10) * time.Millisecond

		taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			time.Sleep(delay)
			return result, nil
		}))
		taskIDs = append(taskIDs, taskID)
	}

	results, err := tm.AwaitAll(ctx, taskIDs)
	assertNoError(t, err)

	if len(results) != len(expectedResults) {
		t.Fatalf("expected %d results, got %d", len(expectedResults), len(results))
	}

	for i, result := range results {
		assertEqual(t, result.Result, expectedResults[i])
	}
}

// TestAwaitAll_WithFailure verifies that if any task fails, AwaitAll returns an error.
func TestAwaitAll_WithFailure(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	taskIDs := []ID{
		tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			return "success", nil
		})),
		tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			return nil, errors.New("task failed")
		})),
	}

	_, err := tm.AwaitAll(ctx, taskIDs)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, ErrTaskFailed) {
		t.Fatalf("expected ErrTaskFailed, got %v", err)
	}
}

// Test AwaitAll with mixed Async and Defer tasks
func TestAwaitAll_MixedAsyncDefer(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	taskIDs := []ID{
		tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			return "async1", nil
		})),
		tm.Defer(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			return "defer1", nil
		})),
	}

	results, err := tm.AwaitAll(ctx, taskIDs)
	assertNoError(t, err)
	assertEqual(t, len(results), 2)
	assertEqual(t, results[0].Result, "async1")
	assertEqual(t, results[1].Result, "defer1")
}

// TestAwaitAny verifies that AwaitAny returns the result of the first task to complete.
func TestAwaitAny(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	taskIDs := []ID{
		tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			time.Sleep(100 * time.Millisecond)
			return "slow", nil
		})),
		tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			time.Sleep(10 * time.Millisecond)
			return "fast", nil
		})),
		tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			time.Sleep(200 * time.Millisecond)
			return "slowest", nil
		})),
	}

	result, err := tm.AwaitAny(ctx, taskIDs)
	assertNoError(t, err)
	assertEqual(t, result.Result, "fast")
}

// Test Status
func TestTaskStatus(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	// Check status transitions
	taskStarted := make(chan struct{})
	taskContinue := make(chan struct{})

	taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		close(taskStarted)
		<-taskContinue
		return "done", nil
	}))

	// Wait for task to start
	<-taskStarted

	status, err := tm.Status(taskID)
	assertNoError(t, err)
	assertEqual(t, status, StatusRunning)

	// Let task complete
	close(taskContinue)

	_, err = tm.Await(ctx, taskID)
	assertNoError(t, err)

	status, err = tm.Status(taskID)
	assertNoError(t, err)
	assertEqual(t, status, StatusCompleted)
}

// Test non-existent task
func TestNonExistentTask(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	fakeID := ID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

	_, err := tm.Await(ctx, fakeID)
	assertError(t, err, ErrTaskNotFound)

	status, err := tm.Status(fakeID)
	assertError(t, err, ErrTaskNotFound)
	assertEqual(t, status, StatusUnknown)
}

// Test panic recovery
func TestPanicRecovery(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		panic("test panic")
	}))

	result, err := tm.Await(ctx, taskID)
	if err == nil {
		t.Fatal("expected error from panicked task, got nil")
	}

	if !errors.Is(err, ErrTaskFailed) {
		t.Fatalf("expected ErrTaskFailed, got %v", err)
	}

	if !errors.Is(result.Error, ErrTaskPanicked) {
		t.Fatalf("expected ErrTaskPanicked in result, got %v", result.Error)
	}
}

// Test idempotent await
func TestIdempotentAwait(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	expected := "test result"
	taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
		return expected, nil
	}))

	// Await multiple times
	result1, err1 := tm.Await(ctx, taskID)
	assertNoError(t, err1)

	result2, err2 := tm.Await(ctx, taskID)
	assertNoError(t, err2)

	// Results should be identical
	assertEqual(t, result1.Result, result2.Result)
	assertEqual(t, result1.ID, result2.ID)
}

// Test concurrent task execution
func TestConcurrentTasks(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	numTasks := 100
	var taskIDs []ID
	results := make([]int, numTasks)

	for i := 0; i < numTasks; i++ {
		idx := i // capture loop variable
		taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			return idx, nil
		}))
		taskIDs = append(taskIDs, taskID)
	}

	// Await all tasks concurrently
	var wg sync.WaitGroup
	for i, taskID := range taskIDs {
		wg.Add(1)
		go func(index int, id ID) {
			defer wg.Done()
			result, err := tm.Await(ctx, id)
			if err != nil {
				t.Errorf("task %d failed: %v", index, err)
				return
			}
			results[index] = result.Result.(int)
		}(i, taskID)
	}

	wg.Wait()

	// Verify all results
	for i := 0; i < numTasks; i++ {
		if results[i] != i {
			t.Errorf("expected result[%d] = %d, got %d", i, i, results[i])
		}
	}
}

// Test Shutdown
func TestShutdown(t *testing.T) {
	tm := NewManager()
	ctx := context.Background()

	// Start several long-running tasks
	numTasks := 10
	var taskIDs []ID
	for i := 0; i < numTasks; i++ {
		taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			select {
			case <-time.After(1 * time.Second):
				return "should not complete", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}))
		taskIDs = append(taskIDs, taskID)
	}

	// Give tasks a moment to start
	time.Sleep(10 * time.Millisecond)

	// Shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	tm.Shutdown(shutdownCtx)

	// Verify tasks are cleaned up
	count := 0
	tm.taskStatuses.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	if count > 0 {
		t.Errorf("expected all tasks to be cleaned up, but found %d remaining", count)
	}

	// Verify we can't await the canceled tasks
	for _, taskID := range taskIDs {
		_, err := tm.Await(context.Background(), taskID)
		if err == nil || !errors.Is(err, ErrTaskNotFound) {
			t.Errorf("expected ErrTaskNotFound for shutdown task, got %v", err)
		}
	}
}

// TestStress_WorkerLimit verifies that the Manager respects a worker concurrency limit.
func TestStress_WorkerLimit(t *testing.T) {
	t.Log("Stress test: verify manager respects worker concurrency limit")
	tm := NewManager(WithWorkerLimit(2))
	ctx := context.Background()

	running := int32(0)
	maxConcurrent := int32(0)

	var taskIDs []ID
	for i := 0; i < 10; i++ {
		taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			current := atomic.AddInt32(&running, 1)

			for {
				max := atomic.LoadInt32(&maxConcurrent)
				if current <= max || atomic.CompareAndSwapInt32(&maxConcurrent, max, current) {
					break
				}
			}

			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&running, -1)
			return nil, nil
		}))
		taskIDs = append(taskIDs, taskID)
	}

	_, err := tm.AwaitAll(ctx, taskIDs)
	assertNoError(t, err)

	if maxConcurrent > 2 {
		t.Errorf("expected max concurrent tasks <= 2, got %d", maxConcurrent)
	}
}

// TestStress_Concurrent stresses the Manager with many concurrent tasks.
func TestStress_Concurrent(t *testing.T) {

	t.Log("Stress test: many tasks concurrently await other tasks and cancellations")

	tm := NewManager(
		func(m *Manager) {
			m.workerLimit = 4 // deliberately low to force contention
			m.workerSemaphore = make(chan struct{}, m.workerLimit)
		},
	)
	ctx := context.Background()

	const numTasks = 100
	taskIDs := make([]ID, numTasks)

	// Step 1: create tasks that sleep a random amount of time
	for i := 0; i < numTasks; i++ {
		taskIDs[i] = tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			time.Sleep(time.Duration(10+rand.Intn(50)) * time.Millisecond)
			return "ok", nil
		}))
	}

	// Step 2: randomly await other tasks in goroutines
	var wg sync.WaitGroup
	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Randomly await 1-3 other tasks
			refs := []ID{}
			for j := 0; j < 3; j++ {
				r := rand.Intn(numTasks)
				if r != idx {
					refs = append(refs, taskIDs[r])
				}
			}

			_, _ = tm.AwaitAll(ctx, refs)
		}(i)
	}

	// Step 3: concurrently cancel random tasks
	for i := 0; i < numTasks/10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := taskIDs[rand.Intn(numTasks)]
			tm.Cancel(id)
		}()
	}

	// Wait for everything to finish
	wg.Wait()

	// Step 4: verify no tasks are stuck in running/pending state
	stats := tm.Stats()
	if stats.Running != 0 && stats.Pending != 0 {
		t.Fatalf("Some tasks are still active: %+v", stats)
	}
}

// TestStress_Apocalypse simulates a "goroutine apocalypse" scenario.
func TestStress_Apocalypse(t *testing.T) {

	t.Log("Stress test: goroutine apocalypse with child tasks and multiple awaits")

	tm := NewManager()
	ctx := context.Background()

	const initialTasks = 50
	const spawnPerTask = 5
	const awaitsPerTask = 3

	var wg sync.WaitGroup

	for i := 0; i < initialTasks; i++ {
		wg.Add(1)
		go func(taskNum int) {
			defer wg.Done()

			taskID := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
				time.Sleep(10 * time.Millisecond)

				// Spawn child tasks
				childIDs := make([]ID, spawnPerTask)
				for j := 0; j < spawnPerTask; j++ {
					childIDs[j] = tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
						time.Sleep(time.Duration(5+taskNum+j) * time.Millisecond)
						return fmt.Sprintf("child-%d-%d", taskNum, j), nil
					}))
				}

				// Random awaits on child tasks
				for k := 0; k < awaitsPerTask; k++ {
					for _, cid := range childIDs {
						_, _ = tm.Await(ctx, cid)
					}
				}

				return fmt.Sprintf("parent-%d", taskNum), nil
			}))

			// Await parent task multiple times concurrently
			innerWg := sync.WaitGroup{}
			innerWg.Add(awaitsPerTask)
			for k := 0; k < awaitsPerTask; k++ {
				go func() {
					defer innerWg.Done()
					_, _ = tm.Await(ctx, taskID)
				}()
			}
			innerWg.Wait()

		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Goroutine apocalypse survived")
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out — possible deadlock")
	}
}

// TestStress_HighConcurrency launches 100,000 tasks concurrently.
func TestStress_HighConcurrency(t *testing.T) {

	t.Log("Stress test: 100,000 tasks concurrently awaited in batches to test throughput")

	tm := NewManager()
	ctx := context.Background()

	const numTasks = 100_000
	taskIDs := make([]ID, numTasks)

	// Launch all tasks
	for i := 0; i < numTasks; i++ {
		idx := i
		taskIDs[i] = tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			// Random short sleep
			time.Sleep(time.Duration(idx%10) * time.Microsecond)
			return idx, nil
		}))
	}

	// Concurrently Await all tasks in batches
	const numWorkers = 100
	results := make([]any, numTasks)
	errs := make([]error, numTasks)
	wg := sync.WaitGroup{}
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			for i := worker; i < numTasks; i += numWorkers {
				res, err := tm.Await(ctx, taskIDs[i])
				results[i] = res.Result
				errs[i] = err
			}
		}(w)
	}

	wg.Wait()

	// Check results
	for i := 0; i < numTasks; i++ {
		if errs[i] != nil {
			t.Fatalf("Task %d failed: %v", i, errs[i])
		}
		if results[i] != i {
			t.Fatalf("Task %d got wrong result: %v", i, results[i])
		}
	}

	t.Logf("All %d tasks completed successfully", numTasks)
}

// TestStress_Shutdown checks that the Manager properly cleans up tasks during Shutdown.
func TestStress_Shutdown(t *testing.T) {

	t.Log("Stress test: ensure shutdown cleans up all running tasks")

	tm := NewManager()
	ctx := context.Background()

	numTasks := 50
	taskIDs := make([]ID, 0, numTasks)

	for i := 0; i < numTasks; i++ {
		id := tm.Async(ctx, RunnableFunc(func(ctx context.Context) (any, error) {
			// Random short delay to simulate work
			select {
			case <-time.After(time.Millisecond * time.Duration(10+rand.Intn(20))):
				return "done", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}))
		taskIDs = append(taskIDs, id)
	}

	// Call Shutdown while tasks are still running
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tm.Shutdown(shutdownCtx)

	// Verify all tasks are cleaned up
	for _, id := range taskIDs {
		_, err := tm.Task(id)
		if err == nil {
			t.Errorf("expected task %s to be cleaned up after shutdown", id.String())
		}
	}

	// Ensure manager shows zero running tasks
	stats := tm.Stats()
	if stats.Total != 0 {
		t.Errorf("expected 0 total tasks after shutdown, got %d", stats.Total)
	}
}
