package asynctask

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/rs/xid"
)

var (
	ErrTaskTimeout  = errors.New("task timed out")
	ErrTaskFailed   = errors.New("task failed")
	ErrTaskNotFound = errors.New("task not found")
	ErrTaskCanceled = errors.New("task canceled")
	ErrTaskPanicked = errors.New("task panicked")
)

const (
	StatusDeferred Status = iota
	StatusPending
	StatusRunning
	StatusCompleted
	StatusFailed
	StatusCanceled
	StatusUnknown
)

type (
	// ID represents a unique identifier for an async task.
	ID xid.ID

	// Status represents the current state of a task
	Status int

	// Task holds the data of an async task
	Task struct {
		ID       ID            `json:"-"`
		Result   any           `json:"-"`
		Time     time.Time     `json:"-"`
		Error    error         `json:"error"`
		Duration time.Duration `json:"duration"`
		Status   string        `json:"status"`
	}

	// Runnable allows any struct to define its own async logic
	Runnable interface {
		Run(ctx context.Context) (any, error)
	}

	// RunnableFunc wraps a function to implement the Runnable interface
	RunnableFunc func(ctx context.Context) (any, error)

	// Manager orchestrates concurrent task execution with worker pool management,
	// task lifecycle tracking, and graceful shutdown. All operations are thread-safe.
	Manager struct {
		tasks        sync.Map // taskID -> *asyncTask or *deferredTask
		tasksResult  sync.Map // taskID -> Task
		tasksCancel  sync.Map // taskID -> context.CancelFunc
		taskStatuses sync.Map // taskID -> Status

		workerLimit     int
		workerSemaphore chan struct{}

		logger *slog.Logger

		mu           sync.Mutex
		wg           sync.WaitGroup
		shuttingDown bool
	}

	// Stats holds the current stats of the task manager
	Stats struct {
		Deferred  int
		Pending   int
		Running   int
		Completed int
		Failed    int
		Canceled  int
		Total     int
	}

	asyncTask struct {
		result Task
		done   chan struct{} // closed when task finishes
		once   sync.Once
	}

	deferredTask struct {
		runnable   Runnable
		ctx        context.Context
		done       chan struct{}
		once       sync.Once
		promotedID ID         // Store the promoted async task ID
		promotedMu sync.Mutex // Protect promotedID access
	}
)

// String returns a string representation of a task ID
func (id ID) String() string {
	return xid.ID(id).String()
}

// Run the wrapped function
func (f RunnableFunc) Run(ctx context.Context) (any, error) {
	return f(ctx)
}

// String returns the string representation of the Status
func (s Status) String() string {
	switch s {
	case StatusDeferred:
		return "deferred"
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusCanceled:
		return "canceled"
	case StatusUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// WithRetry wraps a runnable with exponential backoff retry logic.
// Retries on any error, backoff multiplies by attempt number.
func WithRetry(runnable Runnable, retries int, backoff time.Duration) Runnable {
	return RunnableFunc(func(ctx context.Context) (any, error) {
		var lastErr error
		for i := 0; i <= retries; i++ {
			result, err := runnable.Run(ctx)
			if err == nil {
				return result, nil
			}
			lastErr = err

			// Skip backoff on last attempt
			if i < retries {
				select {
				case <-time.After(backoff * time.Duration(i+1)):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
		return nil, fmt.Errorf("after %d retries: %w", retries, lastErr)
	})
}

// WithTimeout wraps a runnable with deadline enforcement.
// Returns ErrTaskTimeout if runnable exceeds timeout duration.
func WithTimeout(runnable Runnable, timeout time.Duration) Runnable {
	return RunnableFunc(func(ctx context.Context) (any, error) {
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		type result struct {
			value any
			err   error
		}

		resultChan := make(chan result, 1)

		go func() {
			value, err := runnable.Run(timeoutCtx)
			resultChan <- result{value, err}
		}()

		select {
		case res := <-resultChan:
			return res.value, res.err
		case <-timeoutCtx.Done():
			if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("%w: task exceeded %v timeout", ErrTaskTimeout, timeout)
			}
			return nil, timeoutCtx.Err()
		}
	})
}

// NewManager creates a new task manager
func NewManager(opts ...Option) *Manager {
	m := &Manager{
		workerLimit:     runtime.GOMAXPROCS(0) * 24,
		workerSemaphore: make(chan struct{}, runtime.GOMAXPROCS(0)*24),
	}

	// Apply options to customize the manager
	for _, opt := range opts {
		opt(m)
	}

	// Default to a noop logger that discards all messages
	if m.logger == nil {
		m.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return m
}

// Async executes runnable in worker pool, returns task ID immediately.
// Blocks if worker pool is full until slot available or ctx canceled.
func (tm *Manager) Async(ctx context.Context, runnable Runnable) ID {
	taskID := ID(xid.New())
	t := &asyncTask{done: make(chan struct{})}

	tm.tasks.Store(taskID, t)
	tm.taskStatuses.Store(taskID, StatusPending)

	tm.mu.Lock()
	if tm.shuttingDown {
		tm.mu.Unlock()
		tm.taskStatuses.Store(taskID, StatusCanceled)
		close(t.done)
		return taskID
	}
	tm.mu.Unlock()

	select {
	case tm.workerSemaphore <- struct{}{}:
	case <-ctx.Done():
		t.result = Task{ID: taskID, Error: fmt.Errorf("%w", ErrTaskCanceled)}
		close(t.done)
		tm.taskStatuses.Store(taskID, StatusCanceled)
		return taskID
	}

	taskCtx, cancel := context.WithCancel(ctx)
	tm.tasksCancel.Store(taskID, cancel)

	tm.wg.Add(1)

	go func() {
		defer func() { <-tm.workerSemaphore }() // release slot
		defer tm.wg.Done()
		start := time.Now()

		defer func() {
			if r := recover(); r != nil {
				t.result = Task{
					ID:       taskID,
					Error:    fmt.Errorf("%w: %v", ErrTaskPanicked, r),
					Time:     start,
					Duration: time.Since(start),
				}
				tm.tasksResult.Store(taskID, t.result)
				tm.taskStatuses.Store(taskID, StatusFailed)
				close(t.done)
			}
		}()

		tm.taskStatuses.Store(taskID, StatusRunning)
		result, err := runnable.Run(taskCtx)

		status := StatusCompleted
		if err != nil {
			status = StatusFailed
		} else if taskCtx.Err() != nil {
			status = StatusCanceled
			err = fmt.Errorf("%w: %v", ErrTaskCanceled, taskCtx.Err())
		}

		t.result = Task{
			ID:       taskID,
			Result:   result,
			Error:    err,
			Time:     start,
			Duration: time.Since(start),
		}
		tm.taskStatuses.Store(taskID, status)
		tm.tasksResult.Store(taskID, t.result)
		close(t.done)
	}()

	return taskID
}

// Defer creates a task but doesn't execute it until Await is called.
// Task will not consume a worker pool slot until awaited.
func (tm *Manager) Defer(ctx context.Context, runnable Runnable) ID {
	taskID := ID(xid.New())

	tm.mu.Lock()
	if tm.shuttingDown {
		tm.mu.Unlock()
		// Return canceled task immediately if shutting down
		t := &asyncTask{done: make(chan struct{})}
		t.result = Task{ID: taskID, Error: ErrTaskCanceled}
		close(t.done)
		tm.tasks.Store(taskID, t)
		tm.taskStatuses.Store(taskID, StatusCanceled)
		return taskID
	}
	tm.mu.Unlock()

	dt := &deferredTask{
		runnable:   runnable,
		ctx:        ctx,
		done:       make(chan struct{}),
		promotedID: ID{}, // Initialize to zero value
	}

	tm.tasks.Store(taskID, dt)
	tm.taskStatuses.Store(taskID, StatusDeferred)

	return taskID
}

// Await blocks until task completes or ctx canceled. Returns cached result
// for completed tasks. Idempotent - multiple calls return identical results.
// Deferred tasks are promoted to async execution on first await.
func (tm *Manager) Await(ctx context.Context, taskID ID) (Task, error) {
	value, ok := tm.tasks.Load(taskID)
	if !ok {
		return Task{}, ErrTaskNotFound
	}

	// Check if it's a deferred task and promote it to async
	if dt, ok := value.(*deferredTask); ok {
		// Promote deferred to async - only once
		dt.once.Do(func() {
			dt.promotedMu.Lock()
			dt.promotedID = tm.Async(dt.ctx, dt.runnable)
			dt.promotedMu.Unlock()
		})

		// Get the promoted ID and await it
		dt.promotedMu.Lock()
		promotedID := dt.promotedID
		dt.promotedMu.Unlock()

		// Recursively await the promoted async task
		return tm.Await(ctx, promotedID)
	}

	t := value.(*asyncTask)

	select {
	case <-t.done:
		if t.result.Error != nil {
			return t.result, fmt.Errorf("task %s: %w: %w", taskID.String(), ErrTaskFailed, t.result.Error)
		}
		return t.result, nil
	case <-ctx.Done():
		tm.Cancel(taskID)
		// Check if it was a deadline exceeded (timeout) vs cancellation
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Task{}, fmt.Errorf("task %s: %w", taskID.String(), ErrTaskTimeout)
		}
		return Task{}, fmt.Errorf("task %s: %w: %v", taskID.String(), ErrTaskCanceled, ctx.Err())
	}
}

// AwaitAll blocks until all tasks complete or ctx canceled. Returns results
// in same order as taskIDs. Cancels all tasks if ctx canceled. Idempotent.
func (tm *Manager) AwaitAll(ctx context.Context, taskIDs []ID) ([]Task, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}

	var (
		tasks = make([]Task, len(taskIDs))
		errs  = make(chan error, len(taskIDs))
		wg    sync.WaitGroup
	)

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	wg.Add(len(taskIDs))

	// Await each task concurrently
	for i, taskID := range taskIDs {
		go func(index int, id ID) {
			defer wg.Done()

			result, err := tm.Await(cancelCtx, id)
			if err != nil {
				errs <- fmt.Errorf("task %s: %w", id.String(), err)
				return
			}

			tasks[index] = result
		}(i, taskID)
	}

	// Wait for all goroutines to complete or context cancellation
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All tasks completed; check if we have errors
		close(errs)
		if len(errs) > 0 {
			return nil, <-errs
		}
		return tasks, nil

	case <-ctx.Done():
		cancel()

		// Context canceled, so we cancel all tasks
		for _, taskID := range taskIDs {
			tm.Cancel(taskID)
		}
		// Check if it was a deadline exceeded (timeout) vs cancellation
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w", ErrTaskTimeout)
		}
		return nil, fmt.Errorf("%w: %v", ErrTaskCanceled, ctx.Err())
	}
}

// AwaitAny returns first task to complete among taskIDs. Cancels remaining
// tasks once first completes. Returns immediately on first completion.
func (tm *Manager) AwaitAny(ctx context.Context, taskIDs []ID) (Task, error) {
	if len(taskIDs) == 0 {
		return Task{}, nil
	}

	taskChan := make(chan Task, len(taskIDs))
	errChan := make(chan error, len(taskIDs))
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Fire off async waits for each task
	for _, taskID := range taskIDs {
		go func(id ID) {
			task, err := tm.Await(cancelCtx, id)
			if err != nil {
				errChan <- fmt.Errorf("task %s: %w", id.String(), err)
				return
			}
			taskChan <- task
		}(taskID)
	}

	// Wait for the first response, error, or context cancellation
	select {
	case task := <-taskChan:
		cancel()

		// Cancel all tasks except the completed one
		for _, taskID := range taskIDs {
			if task.ID != taskID {
				tm.Cancel(taskID)
			}
		}

		return task, nil

	case err := <-errChan:
		cancel()

		// Cancel all tasks
		for _, taskID := range taskIDs {
			tm.Cancel(taskID)
		}

		return Task{}, err

	case <-ctx.Done():
		cancel()

		// Context canceled, so we cancel all tasks
		for _, taskID := range taskIDs {
			tm.Cancel(taskID)
		}
		// Check if it was a deadline exceeded (timeout) vs cancellation
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Task{}, fmt.Errorf("%w", ErrTaskTimeout)
		}
		return Task{}, fmt.Errorf("%w: %v", ErrTaskCanceled, ctx.Err())
	}
}

// Cancel terminates task by taskID, cleaning up all associated state.
// Returns true if task existed and was canceled, false otherwise.
func (tm *Manager) Cancel(taskID ID) bool {
	// Check if task exists
	_, exists := tm.taskStatuses.Load(taskID)
	if !exists {
		return false
	}

	// Execute the cancel function if present
	if cancelFunc, ok := tm.tasksCancel.Load(taskID); ok {
		cancelFunc.(context.CancelFunc)()
	}

	// Update status and clean up state
	tm.taskStatuses.Store(taskID, StatusCanceled)
	tm.tasksCancel.Delete(taskID)
	tm.tasksResult.Delete(taskID)
	tm.tasks.Delete(taskID)

	tm.logger.Debug("Task Canceled", slog.String("id", taskID.String()))

	return true
}

// Status returns current task status. Returns StatusUnknown and
// ErrTaskNotFound if task doesn't exist.
func (tm *Manager) Status(taskID ID) (Status, error) {
	value, ok := tm.taskStatuses.Load(taskID)
	if !ok {
		return StatusUnknown, ErrTaskNotFound
	}

	status := value.(Status)

	// If it's deferred, check if it's been promoted
	if status == StatusDeferred {
		if taskValue, exists := tm.tasks.Load(taskID); exists {
			if dt, ok := taskValue.(*deferredTask); ok {
				dt.promotedMu.Lock()
				promotedID := dt.promotedID
				dt.promotedMu.Unlock()

				// If promoted, return the promoted task's status
				if promotedID != (ID{}) {
					return tm.Status(promotedID)
				}
			}
		}
	}

	return status, nil
}

// Task retrieves task metadata by ID. Returns partial Task with status
// if task exists but hasn't completed.
func (tm *Manager) Task(taskID ID) (Task, error) {
	// First check if the task exists
	status, ok := tm.taskStatuses.Load(taskID)
	if !ok {
		return Task{Status: StatusUnknown.String()}, ErrTaskNotFound
	}

	// Check if there's a result task in the results map
	if result, ok := tm.tasksResult.Load(taskID); ok {
		task := result.(Task)
		task.Status = status.(Status).String()
		return task, nil
	}

	// Return a task with current status
	return Task{Status: status.(Status).String()}, nil
}

// Prune removes completed/failed/canceled tasks from memory. If ttl > 0,
// only removes tasks finished longer than ttl ago. Returns count pruned.
func (tm *Manager) Prune(ttl time.Duration) int {
	now := time.Now()
	pruned := 0

	tm.taskStatuses.Range(func(key, value any) bool {
		status := value.(Status)
		if status == StatusPending || status == StatusRunning || status == StatusDeferred {
			return true // skip active/deferred tasks
		}

		id := key.(ID)

		// Optionally enforce TTL
		if ttl > 0 {
			if resultVal, ok := tm.tasksResult.Load(id); ok {
				task := resultVal.(Task)
				if !task.Time.IsZero() && now.Sub(task.Time) < ttl {
					return true // skip task, TTL not expired
				}
			}
		}

		// Delete all state
		tm.tasks.Delete(id)
		tm.tasksCancel.Delete(id)
		tm.tasksResult.Delete(id)
		tm.taskStatuses.Delete(id)

		pruned++
		return true
	})

	return pruned
}

// Shutdown cancels all tasks and waits for workers to finish. Returns early
// if ctx canceled during shutdown. Cleans up all internal state.
func (tm *Manager) Shutdown(ctx context.Context) {
	tm.mu.Lock()
	tm.shuttingDown = true
	tm.mu.Unlock()

	// Cancel all tasks concurrently
	tm.taskStatuses.Range(func(key, _ any) bool {
		if cancelFunc, ok := tm.tasksCancel.Load(key); ok {
			cancelFunc.(context.CancelFunc)()
		}
		return true
	})

	done := make(chan struct{})
	go func() {
		tm.wg.Wait() // wait for all tasks to finish
		close(done)
	}()

	select {
	case <-ctx.Done():
		// context canceled, exit early
	case <-done:
		// all tasks finished, now clean up
	}

	// Remove all tasks from internal maps
	tm.tasks.Range(func(key, _ any) bool {
		tm.tasks.Delete(key)
		return true
	})
	tm.tasksCancel.Range(func(key, _ any) bool {
		tm.tasksCancel.Delete(key)
		return true
	})
	tm.tasksResult.Range(func(key, _ any) bool {
		tm.tasksResult.Delete(key)
		return true
	})
	tm.taskStatuses.Range(func(key, _ any) bool {
		tm.taskStatuses.Delete(key)
		return true
	})
}

// Stats returns current task distribution across all statuses.
func (tm *Manager) Stats() Stats {
	var stats Stats

	tm.taskStatuses.Range(func(_, value any) bool {
		stats.Total++
		switch value.(Status) {
		case StatusDeferred:
			stats.Deferred++
		case StatusPending:
			stats.Pending++
		case StatusRunning:
			stats.Running++
		case StatusCompleted:
			stats.Completed++
		case StatusFailed:
			stats.Failed++
		case StatusCanceled:
			stats.Canceled++
		}
		return true
	})

	return stats
}
