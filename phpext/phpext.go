package phpext

// #include <stdlib.h>
// #include <stdint.h>
// #cgo CFLAGS: -I../../frankenphp
// #include "frankenphp.h"
// #include "phpext.h"
//
import "C"
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"frankenasync/asynctask"

	"github.com/dunglas/frankenphp"

	"github.com/rs/xid"
)

// DocumentRoot is set by the application to pass to subrequests.
var DocumentRoot string

// Register hooks our PHP module into FrankenPHP's extension loading.
func Register() {
	C.frankenasync_register()
}

// scriptRequest is the JSON payload from PHP for script execution.
type scriptRequest struct {
	Name string     `json:"name"`
	Env  *scriptEnv `json:"env,omitempty"`
}

type scriptEnv struct {
	App map[string]any    `json:"app,omitempty"`
	CGI map[string]string `json:"cgi,omitempty"`
}

// scriptResult is the JSON response returned to PHP.
type scriptResult struct {
	Name     string            `json:"name"`
	Body     string            `json:"body"`
	Headers  map[string]string `json:"headers"`
	Status   int               `json:"status"`
	Duration float64           `json:"duration"` // milliseconds
}

// responseRecorder is a minimal http.ResponseWriter that captures output.
type responseRecorder struct {
	code      int
	headerMap http.Header
	body      *bytes.Buffer
	wrote     bool
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{
		code:      200,
		headerMap: make(http.Header),
		body:      new(bytes.Buffer),
	}
}

func (r *responseRecorder) Header() http.Header     { return r.headerMap }
func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.wrote = true
	}
	return r.body.Write(b)
}
func (r *responseRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.code = code
		r.wrote = true
	}
}

// executeScript runs a PHP script as a subrequest via FrankenPHP.
func executeScript(ctx context.Context, sr *scriptRequest) (*scriptResult, error) {
	start := time.Now()

	thread, ok := frankenphp.Thread(threadIndexFromContext(ctx))
	if !ok || thread.IsRequestDone() {
		return nil, fmt.Errorf("thread not available")
	}

	// Clone the original request and update the URL path
	origReq := thread.Request
	clonedReq := origReq.Clone(ctx)

	// PHP's php_resolve_path may return an absolute path; strip the document root
	scriptPath := sr.Name
	if DocumentRoot != "" && strings.HasPrefix(scriptPath, DocumentRoot) {
		scriptPath = strings.TrimPrefix(scriptPath, DocumentRoot)
	}
	clonedReq.URL.Path = "/" + strings.TrimPrefix(scriptPath, "/")

	// Prepare CGI environment variables
	envCGI := make(map[string]string)
	if sr.Env != nil {
		for key, value := range sr.Env.CGI {
			envCGI[strings.ToUpper(strings.ReplaceAll(key, "-", "_"))] = value
		}
		// Pass app variables as APP_* server variables
		for key, value := range sr.Env.App {
			envCGI["APP_"+strings.ToUpper(strings.ReplaceAll(fmt.Sprint(key), "-", "_"))] = fmt.Sprint(value)
		}
	}

	// Create FrankenPHP request for the subrequest
	reqOpts := []frankenphp.RequestOption{
		frankenphp.WithRequestEnv(envCGI),
		frankenphp.WithOriginalRequest(origReq),
	}
	if DocumentRoot != "" {
		reqOpts = append(reqOpts, frankenphp.WithRequestResolvedDocumentRoot(DocumentRoot))
	}
	req, err := frankenphp.NewRequestWithContext(clonedReq, reqOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare subrequest for '%s': %w", sr.Name, err)
	}

	// Execute via FrankenPHP
	rec := newResponseRecorder()
	if err := frankenphp.ServeHTTP(rec, req); err != nil {
		return nil, fmt.Errorf("failed to execute script '%s': %w", sr.Name, err)
	}

	// Collect response headers
	headers := make(map[string]string, len(rec.headerMap))
	for key, values := range rec.headerMap {
		headers[key] = strings.Join(values, ",")
	}

	elapsed := time.Since(start)

	return &scriptResult{
		Name:     sr.Name,
		Body:     rec.body.String(),
		Headers:  headers,
		Status:   rec.code,
		Duration: float64(elapsed.Microseconds()) / 1000.0,
	}, nil
}

// threadIndexKey is used to pass the thread index through context.
type threadIndexKey struct{}

func withThreadIndex(ctx context.Context, index int) context.Context {
	return context.WithValue(ctx, threadIndexKey{}, index)
}

func threadIndexFromContext(ctx context.Context) int {
	if idx, ok := ctx.Value(threadIndexKey{}).(int); ok {
		return idx
	}
	return -1
}

//export go_execute_script
func go_execute_script(threadIndex C.uintptr_t, script_json *C.char) (*C.char, C.bool) {
	thread, ok := frankenphp.Thread(int(threadIndex))
	if !ok || thread.IsRequestDone() {
		return C.CString("Thread not available"), C.bool(false)
	}

	ctx := thread.Request.Context()
	ctx = withThreadIndex(ctx, int(threadIndex))

	strScript := C.GoString(script_json)

	var sr scriptRequest
	if err := json.Unmarshal([]byte(strScript), &sr); err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	result, err := executeScript(ctx, &sr)
	if err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	return C.CString(string(resultJSON)), C.bool(true)
}

//export go_execute_script_async
func go_execute_script_async(threadIndex C.uintptr_t, script_json *C.char) (*C.char, C.bool) {
	thread, ok := frankenphp.Thread(int(threadIndex))
	if !ok || thread.IsRequestDone() {
		return C.CString("Thread not available"), C.bool(false)
	}

	ctx := thread.Request.Context()
	ctx = withThreadIndex(ctx, int(threadIndex))

	strScript := C.GoString(script_json)

	var sr scriptRequest
	if err := json.Unmarshal([]byte(strScript), &sr); err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	tasks := asynctask.FromContext(ctx)
	taskID := tasks.Async(ctx, asynctask.RunnableFunc(func(ctx context.Context) (any, error) {
		result, err := executeScript(ctx, &sr)
		if err != nil {
			return nil, err
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return string(resultJSON), nil
	}))

	return C.CString(taskID.String()), C.bool(true)
}

//export go_execute_script_defer
func go_execute_script_defer(threadIndex C.uintptr_t, script_json *C.char) (*C.char, C.bool) {
	thread, ok := frankenphp.Thread(int(threadIndex))
	if !ok || thread.IsRequestDone() {
		return C.CString("Thread not available"), C.bool(false)
	}

	ctx := thread.Request.Context()
	ctx = withThreadIndex(ctx, int(threadIndex))

	strScript := C.GoString(script_json)

	var sr scriptRequest
	if err := json.Unmarshal([]byte(strScript), &sr); err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	tasks := asynctask.FromContext(ctx)
	taskID := tasks.Defer(ctx, asynctask.RunnableFunc(func(ctx context.Context) (any, error) {
		result, err := executeScript(ctx, &sr)
		if err != nil {
			return nil, err
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return string(resultJSON), nil
	}))

	return C.CString(taskID.String()), C.bool(true)
}

//export go_asynctask_await
func go_asynctask_await(threadIndex C.uintptr_t, task_id *C.char, timeout C.int) (*C.char, C.bool) {
	thread, ok := frankenphp.Thread(int(threadIndex))
	if !ok || thread.IsRequestDone() {
		return C.CString("Thread not available"), C.bool(false)
	}

	strTaskID := C.GoString(task_id)
	xidTaskID, err := xid.FromString(strTaskID)
	if err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	ctx := thread.Request.Context()
	tasks := asynctask.FromContext(ctx)

	var cancel context.CancelFunc
	if timeout > 0 {
		durTimeout := time.Duration(timeout) * time.Millisecond
		ctx, cancel = context.WithTimeout(thread.Request.Context(), durTimeout)
		defer cancel()
	}

	result, err := tasks.Await(ctx, asynctask.ID(xidTaskID))
	if err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	var resultStr string
	switch v := result.Result.(type) {
	case string:
		resultStr = v
	case []byte:
		resultStr = string(v)
	default:
		taskJSON, err := json.Marshal(result.Result)
		if err != nil {
			return C.CString(err.Error()), C.bool(false)
		}
		resultStr = string(taskJSON)
	}

	return C.CString(resultStr), C.bool(true)
}

//export go_asynctask_await_all
func go_asynctask_await_all(threadIndex C.uintptr_t, task_id_json *C.char, timeout C.int) (*C.char, C.bool) {
	thread, ok := frankenphp.Thread(int(threadIndex))
	if !ok || thread.IsRequestDone() {
		return C.CString("Thread not available"), C.bool(false)
	}

	strTaskIDs := C.GoString(task_id_json)

	var arrTaskIDs []string
	if err := json.Unmarshal([]byte(strTaskIDs), &arrTaskIDs); err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	taskIDs := make([]asynctask.ID, 0, len(arrTaskIDs))
	for _, idStr := range arrTaskIDs {
		xidID, err := xid.FromString(idStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid task ID: %s", idStr)), C.bool(false)
		}
		taskIDs = append(taskIDs, asynctask.ID(xidID))
	}

	ctx := thread.Request.Context()
	tasks := asynctask.FromContext(ctx)

	var cancel context.CancelFunc
	if timeout > 0 {
		durTimeout := time.Duration(timeout) * time.Millisecond
		ctx, cancel = context.WithTimeout(thread.Request.Context(), durTimeout)
		defer cancel()
	}

	results, err := tasks.AwaitAll(ctx, taskIDs)
	if err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	data := make([]any, 0, len(results))
	for _, res := range results {
		switch v := res.Result.(type) {
		case string:
			data = append(data, v)
		case []byte:
			data = append(data, string(v))
		default:
			data = append(data, v)
		}
	}

	tasksJSON, err := json.Marshal(data)
	if err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	return C.CString(string(tasksJSON)), C.bool(true)
}

//export go_asynctask_await_any
func go_asynctask_await_any(threadIndex C.uintptr_t, task_id_json *C.char, timeout C.int) (*C.char, C.bool) {
	thread, ok := frankenphp.Thread(int(threadIndex))
	if !ok || thread.IsRequestDone() {
		return C.CString("Thread not available"), C.bool(false)
	}

	strTaskIDs := C.GoString(task_id_json)

	var arrTaskIDs []string
	if err := json.Unmarshal([]byte(strTaskIDs), &arrTaskIDs); err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	taskIDs := make([]asynctask.ID, 0, len(arrTaskIDs))
	for _, idStr := range arrTaskIDs {
		xidID, err := xid.FromString(idStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid task ID: %s", idStr)), C.bool(false)
		}
		taskIDs = append(taskIDs, asynctask.ID(xidID))
	}

	ctx := thread.Request.Context()
	tasks := asynctask.FromContext(ctx)

	var cancel context.CancelFunc
	if timeout > 0 {
		durTimeout := time.Duration(timeout) * time.Millisecond
		ctx, cancel = context.WithTimeout(thread.Request.Context(), durTimeout)
		defer cancel()
	}

	result, err := tasks.AwaitAny(ctx, taskIDs)
	if err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	var resultStr string
	switch v := result.Result.(type) {
	case string:
		resultStr = v
	case []byte:
		resultStr = string(v)
	default:
		taskJSON, err := json.Marshal(result.Result)
		if err != nil {
			return C.CString(err.Error()), C.bool(false)
		}
		resultStr = string(taskJSON)
	}

	return C.CString(resultStr), C.bool(true)
}

//export go_asynctask_info
func go_asynctask_info(threadIndex C.uintptr_t, task_id *C.char) (*C.char, C.bool) {
	thread, ok := frankenphp.Thread(int(threadIndex))
	if !ok || thread.IsRequestDone() {
		return C.CString("Thread not available"), C.bool(false)
	}

	strTaskID := C.GoString(task_id)
	xidTaskID, err := xid.FromString(strTaskID)
	if err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	ctx := thread.Request.Context()
	tasks := asynctask.FromContext(ctx)

	taskData, err := tasks.Task(asynctask.ID(xidTaskID))
	if err != nil {
		if errors.Is(err, asynctask.ErrTaskNotFound) {
			return nil, C.bool(true)
		}
		return C.CString(err.Error()), C.bool(false)
	}

	// Build a JSON-serializable response with duration in milliseconds
	type taskInfo struct {
		Status   string  `json:"status"`
		Duration float64 `json:"duration"`
		Error    string  `json:"error,omitempty"`
	}

	info := taskInfo{
		Status:   taskData.Status,
		Duration: float64(taskData.Duration.Microseconds()) / 1000.0,
	}
	if taskData.Error != nil {
		info.Error = taskData.Error.Error()
	}

	byteResult, err := json.Marshal(info)
	if err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	return C.CString(string(byteResult)), C.bool(true)
}

//export go_asynctask_cancel
func go_asynctask_cancel(threadIndex C.uintptr_t, task_id *C.char) (*C.char, C.bool) {
	thread, ok := frankenphp.Thread(int(threadIndex))
	if !ok || thread.IsRequestDone() {
		return C.CString("Thread not available"), C.bool(false)
	}

	strTaskID := C.GoString(task_id)
	xidTaskID, err := xid.FromString(strTaskID)
	if err != nil {
		return C.CString(err.Error()), C.bool(false)
	}

	ctx := thread.Request.Context()
	tasks := asynctask.FromContext(ctx)
	result := tasks.Cancel(asynctask.ID(xidTaskID))

	return nil, C.bool(result)
}

//export go_parse_duration_ms
func go_parse_duration_ms(input *C.char) C.longlong {
	if input == nil {
		return -1
	}

	str := C.GoString(input)
	d, err := time.ParseDuration(str)
	if err != nil {
		return -1
	}

	if d < time.Millisecond {
		return -1
	}

	return C.longlong(d.Milliseconds())
}
