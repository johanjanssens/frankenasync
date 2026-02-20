#!/usr/bin/env bash
#
# bench.sh — Automated test & benchmark for FrankenAsync two-level concurrency
#
# Usage:
#   ./bench.sh              # build, start server, run tests, stop server
#   ./bench.sh --no-build   # skip build, assume server already running
#   ./bench.sh --keep       # don't stop the server after tests
#

set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
PORT="${FRANKENASYNC_PORT:-8081}"
BASE="http://localhost:${PORT}"
LOG_FILE="${ROOT}/.bench-server.log"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

PASS=0
FAIL=0
SKIP=0
BUILD=1
KEEP=0
STARTED_SERVER=0

for arg in "$@"; do
    case "$arg" in
        --no-build) BUILD=0 ;;
        --keep)     KEEP=1 ;;
    esac
done

# ─── Helpers ──────────────────────────────────────────────────────────────────

log()  { echo -e "${CYAN}[bench]${NC} $*"; }
pass() { echo -e "  ${GREEN}PASS${NC} $*"; PASS=$((PASS + 1)); }
fail() { echo -e "  ${RED}FAIL${NC} $*"; FAIL=$((FAIL + 1)); }
skip() { echo -e "  ${YELLOW}SKIP${NC} $*"; SKIP=$((SKIP + 1)); }

cleanup() {
    if [ "$KEEP" -eq 0 ] && [ "$STARTED_SERVER" -eq 1 ]; then
        log "Stopping server..."
        lsof -ti :"$PORT" | xargs kill 2>/dev/null || true
        sleep 1
    fi
    rm -f "$LOG_FILE"
}
trap cleanup EXIT

wait_for_server() {
    local max_wait=15
    local waited=0
    while ! curl -sf "${BASE}/?n=1&threads=1&local=1" >/dev/null 2>&1; do
        sleep 1
        waited=$((waited + 1))
        if [ "$waited" -ge "$max_wait" ]; then
            echo -e "${RED}Server failed to start after ${max_wait}s${NC}"
            if [ -f "$LOG_FILE" ]; then
                echo "Server log:"
                tail -20 "$LOG_FILE"
            fi
            exit 1
        fi
    done
}

# Fetch a URL and measure wall-clock time. Sets $BODY, $HTTP_CODE, $WALL_MS.
fetch() {
    local url="$1"
    local start end
    start=$(python3 -c 'import time; print(int(time.time()*1000))')
    BODY=$(curl -sf -w '\n%{http_code}' --max-time 30 "$url" 2>/dev/null) || { BODY=""; HTTP_CODE=0; WALL_MS=0; return 1; }
    end=$(python3 -c 'import time; print(int(time.time()*1000))')
    HTTP_CODE=$(echo "$BODY" | tail -1)
    BODY=$(echo "$BODY" | sed '$d')
    WALL_MS=$((end - start))
    return 0
}

# Count occurrences of a pattern in $BODY
count_matches() {
    echo "$BODY" | grep -o "$1" | wc -l | tr -d ' '
}

# ─── Build & Start ───────────────────────────────────────────────────────────

if [ "$BUILD" -eq 1 ]; then
    log "Building..."
    cd "$ROOT"
    make build 2>&1 | tail -1

    # Kill any existing server on our port
    lsof -ti :"$PORT" | xargs kill 2>/dev/null || true
    sleep 1

    log "Starting server on :${PORT}..."
    cd "$ROOT" && ./dist/frankenasync > "$LOG_FILE" 2>&1 &
    STARTED_SERVER=1
    wait_for_server
    log "Server ready"
else
    log "Skipping build (--no-build)"
    if ! curl -sf "${BASE}/?n=1&threads=1&local=1" >/dev/null 2>&1; then
        echo -e "${RED}Server not running on ${BASE}${NC}"
        exit 1
    fi
fi

echo ""
echo -e "${BOLD}═══════════════════════════════════════════════════════${NC}"
echo -e "${BOLD}  FrankenAsync — Two-Level Concurrency Tests${NC}"
echo -e "${BOLD}═══════════════════════════════════════════════════════${NC}"
echo ""

# ─── Test 1: Basic smoke test ────────────────────────────────────────────────

log "Test 1: Smoke test (n=5, threads=2)"
if fetch "${BASE}/?n=5&threads=2&local=1"; then
    cards=$(count_matches 'data-load-time=')
    if [ "$cards" -eq 5 ]; then
        pass "5 cards rendered"
    else
        fail "Expected 5 cards, got $cards"
    fi
else
    fail "Request failed (HTTP $HTTP_CODE)"
fi

# ─── Test 2: Single thread, all coroutines ───────────────────────────────────

log "Test 2: Single thread, coroutines only (n=10, threads=1)"
if fetch "${BASE}/?n=10&threads=1&local=1"; then
    cards=$(count_matches 'data-load-time=')
    if [ "$cards" -eq 10 ]; then
        pass "10 cards rendered with 1 thread"
    else
        fail "Expected 10 cards, got $cards"
    fi

    # With 1 thread and 10 tasks sleeping 100-500ms each, wall clock should be
    # well under 10*500ms=5000ms thanks to coroutines (expect ~500ms).
    if [ "$WALL_MS" -lt 3000 ]; then
        pass "Wall clock ${WALL_MS}ms < 3000ms (coroutines working)"
    else
        fail "Wall clock ${WALL_MS}ms >= 3000ms (coroutines may not be working)"
    fi
else
    fail "Request failed (HTTP $HTTP_CODE)"
fi

# ─── Test 3: Multiple threads with coroutines ────────────────────────────────

log "Test 3: Two-level concurrency (n=50, threads=5)"
if fetch "${BASE}/?n=50&threads=5&local=1"; then
    cards=$(count_matches 'data-load-time=')
    if [ "$cards" -eq 50 ]; then
        pass "50 cards rendered across 5 threads"
    else
        fail "Expected 50 cards, got $cards"
    fi

    if [ "$WALL_MS" -lt 5000 ]; then
        pass "Wall clock ${WALL_MS}ms < 5000ms"
    else
        fail "Wall clock ${WALL_MS}ms >= 5000ms (unexpectedly slow)"
    fi
else
    fail "Request failed (HTTP $HTTP_CODE)"
fi

# ─── Test 4: Default params (n=100, threads=10) ─────────────────────────────

log "Test 4: Default params (n=100, threads=10)"
if fetch "${BASE}/?n=100&threads=10&local=1"; then
    cards=$(count_matches 'data-load-time=')
    if [ "$cards" -eq 100 ]; then
        pass "100 cards rendered"
    else
        fail "Expected 100 cards, got $cards"
    fi

    # Footer should show thread count
    if echo "$BODY" | grep -q "10 threads"; then
        pass "Footer shows 10 threads"
    else
        skip "Footer thread count not found (non-critical)"
    fi
else
    fail "Request failed (HTTP $HTTP_CODE)"
fi

# ─── Test 5: Stability — repeated requests ──────────────────────────────────

log "Test 5: Stability (20 repeated requests, n=20, threads=5)"
stable=0
for i in $(seq 1 20); do
    if fetch "${BASE}/?n=20&threads=5&local=1"; then
        cards=$(count_matches 'data-load-time=')
        if [ "$cards" -eq 20 ]; then
            stable=$((stable + 1))
        fi
    fi
done
if [ "$stable" -eq 20 ]; then
    pass "All 20 requests returned 20 cards"
else
    fail "Only $stable/20 requests succeeded"
fi

# ─── Test 6: Edge case — n=1 ────────────────────────────────────────────────

log "Test 6: Edge case (n=1, threads=1)"
if fetch "${BASE}/?n=1&threads=1&local=1"; then
    cards=$(count_matches 'data-load-time=')
    if [ "$cards" -eq 1 ]; then
        pass "1 card rendered"
    else
        fail "Expected 1 card, got $cards"
    fi
else
    fail "Request failed (HTTP $HTTP_CODE)"
fi

# ─── Test 7: threads > n (should clamp) ─────────────────────────────────────

log "Test 7: More threads than tasks (n=3, threads=10)"
if fetch "${BASE}/?n=3&threads=10&local=1"; then
    cards=$(count_matches 'data-load-time=')
    if [ "$cards" -eq 3 ]; then
        pass "3 cards rendered (threads clamped to 3)"
    else
        fail "Expected 3 cards, got $cards"
    fi
else
    fail "Request failed (HTTP $HTTP_CODE)"
fi

# ─── Test 8: Speedup benchmark ──────────────────────────────────────────────

log "Test 8: Speedup — aggregated IO vs wall clock (n=500, threads=10)"

if fetch "${BASE}/?n=500&threads=10&local=1"; then
    cards=$(count_matches 'data-load-time=')
    if [ "$cards" -eq 500 ]; then
        pass "500 cards rendered"
    else
        fail "Expected 500 cards, got $cards"
    fi

    # Sum all individual load times (= sequential estimate)
    io_total=$(echo "$BODY" | grep -o 'data-load-time="[0-9.]*"' | sed 's/[^0-9.]//g' | python3 -c "
import sys
total = sum(float(line) for line in sys.stdin if line.strip())
print(f'{total:.0f}')
")

    io_secs=$(python3 -c "print(f'{$io_total / 1000:.1f}')")
    wall_secs=$(python3 -c "print(f'{$WALL_MS / 1000:.1f}')")
    speedup=$(python3 -c "print(f'{$io_total / $WALL_MS:.0f}')")

    if [ "$speedup" -ge 100 ]; then
        pass "Speedup: ${speedup}x (${io_secs}s IO / ${wall_secs}s wall)"
    else
        fail "Speedup: ${speedup}x < 100x (${io_secs}s IO / ${wall_secs}s wall)"
    fi
else
    fail "Request failed (HTTP $HTTP_CODE)"
    fail "Could not measure speedup"
fi

# ─── Test 9: HTTP mode — local API endpoint ──────────────────────────────────

log "Test 9: HTTP mode with local API (n=100, threads=10, local=0)"

if fetch "${BASE}/?n=100&threads=10&local=0"; then
    cards=$(count_matches 'data-load-time=')
    if [ "$cards" -eq 100 ]; then
        pass "100 cards rendered via HTTP"
    else
        fail "Expected 100 cards via HTTP, got $cards"
    fi

    # Each API call takes 50-150ms, so aggregated IO should be ~5-15s
    io_total=$(echo "$BODY" | grep -o 'data-load-time="[0-9.]*"' | sed 's/[^0-9.]//g' | python3 -c "
import sys
total = sum(float(line) for line in sys.stdin if line.strip())
print(f'{total:.0f}')
")
    speedup=$(python3 -c "print(f'{$io_total / $WALL_MS:.0f}')")
    if [ "$speedup" -ge 5 ]; then
        pass "HTTP speedup: ${speedup}x (wall ${WALL_MS}ms)"
    else
        fail "HTTP speedup: ${speedup}x < 5x (wall ${WALL_MS}ms)"
    fi
else
    fail "HTTP request failed (HTTP $HTTP_CODE)"
    fail "Could not measure HTTP speedup"
fi

# ─── Test 10: HTTP mode at scale ─────────────────────────────────────────────

log "Test 10: HTTP mode at scale (n=500, threads=10, local=0)"

if fetch "${BASE}/?n=500&threads=10&local=0"; then
    cards=$(count_matches 'data-load-time=')
    if [ "$cards" -eq 500 ]; then
        pass "500 cards rendered via HTTP"
    else
        fail "Expected 500 cards via HTTP, got $cards"
    fi
else
    fail "HTTP request failed at scale (HTTP $HTTP_CODE)"
fi

# ─── Summary ─────────────────────────────────────────────────────────────────

echo ""
echo -e "${BOLD}═══════════════════════════════════════════════════════${NC}"
TOTAL=$((PASS + FAIL + SKIP))
echo -e "  ${GREEN}${PASS} passed${NC}, ${RED}${FAIL} failed${NC}, ${YELLOW}${SKIP} skipped${NC} (${TOTAL} checks)"
echo -e "${BOLD}═══════════════════════════════════════════════════════${NC}"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
