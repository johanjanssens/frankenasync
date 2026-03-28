package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/johanjanssens/frankenasync/asynctask"
	"github.com/johanjanssens/frankenasync/phpext"

	"github.com/dunglas/frankenphp"
	"github.com/joho/godotenv"
	"github.com/lmittmann/tint"
)

func main() {
	// Load .env if present
	_ = godotenv.Load()

	// Set up logger
	logger := slog.New(tint.NewHandler(os.Stdout, &tint.Options{
		Level:      slog.LevelDebug,
		TimeFormat: time.Kitchen,
	}))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Resolve document root
	docRoot, err := filepath.Abs("examples")
	if err != nil {
		logger.Error("Failed to resolve document root", "error", err)
		os.Exit(1)
	}

	// Register PHP extension
	phpext.Register()
	phpext.DocumentRoot = docRoot

	// Thread pool: starts with numThreads, autoscales up to maxThreads under load.
	// The worker semaphore is capped at maxThreads-2 so subrequests flow to
	// FrankenPHP and trigger autoscaling when threads are saturated.
	numCPU := runtime.NumCPU()
	numThreads := numCPU * 4
	if v := os.Getenv("FRANKENASYNC_THREADS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			numThreads = n
		}
	}

	maxThreads := numThreads * 4
	workerLimit := maxThreads - 2
	if v := os.Getenv("FRANKENASYNC_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workerLimit = n
		}
	}
	if workerLimit > maxThreads-2 {
		logger.Warn("Capping worker limit to max thread pool size", "requested", workerLimit, "capped", maxThreads-2)
		workerLimit = maxThreads - 2
	}

	phpIni := map[string]string{
		"opcache.enable":               "1",
		"opcache.enable_file_override": "1",
		"opcache.validate_timestamps":  "0",
		"include_path":                 docRoot,
		"swow.enable":                  "0",
	}

	// Init FrankenPHP with ImmediatePolicy for instant thread scaling.
	// Internal async subrequests are tagged "async" (see phpext) so the policy
	// can differentiate them from external HTTP traffic.
	initOptions := []frankenphp.Option{
		frankenphp.WithNumThreads(numThreads),
		frankenphp.WithMaxThreads(maxThreads),
		frankenphp.WithScalingPolicy(frankenphp.NewImmediateScalingPolicy()),
		frankenphp.WithLogger(logger),
		frankenphp.WithPhpIni(phpIni),
	}

	if err := frankenphp.Init(initOptions...); err != nil {
		logger.Error("Failed to initialize FrankenPHP", "error", err)
		os.Exit(1)
	}
	defer frankenphp.Shutdown()

	// Set up HTTP handler
	addr := ":8081"
	if port := os.Getenv("FRANKENASYNC_PORT"); port != "" {
		addr = ":" + port
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Local API endpoint — simulates JSONPlaceholder with realistic latency
		if strings.HasPrefix(r.URL.Path, "/api/comments/") {
			idStr := strings.TrimPrefix(r.URL.Path, "/api/comments/")
			id, err := strconv.Atoi(idStr)
			if err != nil || id < 1 {
				http.Error(w, "Invalid comment ID", http.StatusBadRequest)
				return
			}

			delay := time.Duration(50+rand.Intn(100)) * time.Millisecond
			time.Sleep(delay)

			names := []string{
				"id labore ex et quam laborum",
				"quo vero reiciendis velit similique earum",
				"odio adipisci rerum aut animi",
				"alias odio sit",
				"vero eaque aliquid doloribus et culpa",
				"et fugit eligendi deleniti quidem qui sint nihil autem",
				"repellat consequatur praesentium vel minus",
				"et omnis dolorem",
				"provident id voluptas",
				"eaque et deleniti atque tenetur ut quo ut",
			}

			comment := map[string]any{
				"postId": ((id - 1) / 5) + 1,
				"id":     id,
				"name":   names[(id-1)%len(names)],
				"email":  fmt.Sprintf("user%d@example.com", id),
				"body":   fmt.Sprintf("Comment body for comment %d", id),
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(comment)
			return
		}

		// Rewrite directory requests to index.php
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, "/") {
			r.URL.Path = r.URL.Path + "index.php"
		}

		// Create async task manager for this request
		taskManager := asynctask.NewManager(
			asynctask.WithWorkerLimit(workerLimit),
			asynctask.WithLogger(logger.Handler()),
		)

		// Store manager in request context
		reqCtx := asynctask.WithContext(r.Context(), taskManager)
		r = r.WithContext(reqCtx)

		// Create FrankenPHP request
		req, err := frankenphp.NewRequestWithContext(r,
			frankenphp.WithRequestResolvedDocumentRoot(docRoot),
			frankenphp.WithRequestLogger(logger),
		)
		if err != nil {
			logger.Error("Failed to create FrankenPHP request", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if err := frankenphp.ServeHTTP(w, req); err != nil {
			logger.Error("Failed to serve PHP", "error", err)
		}

		// Shutdown task manager after request completes
		taskManager.Shutdown(r.Context())
	})

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		logger.Info("Starting FrankenAsync server", "addr", addr, "threads", numThreads, "workers", workerLimit, "cpus", numCPU)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Server error", "error", err)
			cancel()
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("Shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Failed to shutdown server", "error", err)
	}
}
