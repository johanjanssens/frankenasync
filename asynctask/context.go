package asynctask

import "context"

type ctxKey struct{}

// WithContext stores an async task Manager in the context and returns
// a new derived context containing it.
func WithContext(ctx context.Context, manager *Manager) context.Context {
	return context.WithValue(ctx, ctxKey{}, manager)
}

// FromContext retrieves the async task Manager from the provided context.
// If no Manager is found in the context, it creates and returns a new default
// Manager instance.
func FromContext(ctx context.Context) *Manager {
	if manager, ok := ctx.Value(ctxKey{}).(*Manager); ok {
		return manager
	}
	return NewManager()
}
