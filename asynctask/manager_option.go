package asynctask

import "log/slog"

type (
	Option func(*Manager)
)

// WithWorkerLimit sets the maximum number of concurrent workers in the pool.
func WithWorkerLimit(limit int) Option {
	return func(m *Manager) {
		if limit > 0 {
			m.workerLimit = limit
			m.workerSemaphore = make(chan struct{}, limit)
		}
	}
}

// WithLogger sets a custom logger for the Manager.
func WithLogger(handler slog.Handler) Option {
	return func(m *Manager) {
		m.logger = slog.New(handler)
	}
}
