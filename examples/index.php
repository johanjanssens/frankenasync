<?php
use Frankenphp\Script;
use Frankenphp\Async\Task;

header('Content-Type: text/html');

// Configuration
$total      = (int)($_GET['n'] ?? 100);          // Total tasks (comment fetches)
$threads    = (int)($_GET['threads'] ?? 10);     // FrankenPHP threads (batches)
$local      = (int)($_GET['local'] ?? 1);        // 1 = local mock, 0 = external API
$coroutines = (int)($_GET['coroutines'] ?? 1);   // 0 = blocking IO (1 task per thread)

// Wrap IDs to 1-500 range for JSONPlaceholder API
$allIds = array_map(fn($id) => (($id - 1) % 500) + 1, range(1, $total));

if ($coroutines) {
    // Two-level: batch tasks across threads, coroutines within each thread
    $threads = min($threads, $total);
    $batchSize = (int)ceil($total / $threads);
    $batches = array_chunk($allIds, $batchSize);
    $actualThreads = count($batches);
    $coroutinesPerThread = $batchSize;

    $tasks = [];
    foreach ($batches as $batch) {
        $tasks[] = (new Script('include/worker.php'))->async([
            "batch_ids" => json_encode($batch),
            "local"     => $local,
        ]);
    }
} else {
    // Pure threads: 1 task per thread, blocking IO, no coroutines
    $actualThreads = $total;
    $coroutinesPerThread = 0;

    $tasks = [];
    foreach ($allIds as $id) {
        $tasks[] = (new Script('include/task.php'))->async([
            "id"    => $id,
            "local" => $local,
        ]);
    }
}

// Await all threads
$results = Task::awaitAll($tasks, "60s");

// Flatten results from all batches/threads
$comments = [];
foreach ($results as $result) {
    $decoded = is_string($result) ? json_decode($result, true) : $result;
    $body = $decoded["body"] ?? '';
    $parsed = json_decode($body, true);
    if (is_array($parsed)) {
        if (isset($parsed['id'])) {
            // Single result from task.php
            $comments[] = $parsed;
        } else {
            // Array of results from worker.php
            foreach ($parsed as $comment) {
                if (is_array($comment) && isset($comment['id'])) {
                    $comments[] = $comment;
                }
            }
        }
    }
}

$mode = $coroutinesPerThread ? "{$actualThreads} threads &times; ~{$coroutinesPerThread} coroutines" : "{$actualThreads} threads (blocking)";
?>
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>FrankenAsync</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: #f5f5f5;
            color: #333;
        }

        /* ── Hero stats banner ─────────────────────────────────── */

        .hero {
            background: #1a1a2e;
            color: #fff;
            padding: 2rem 2rem 1.5rem;
        }

        .hero h1 {
            font-size: 1.4rem;
            font-weight: 600;
            margin-bottom: 1.2rem;
            color: #e0e0e0;
        }

        .hero h1 strong {
            color: #fff;
        }

        .stats {
            display: flex;
            gap: 2.5rem;
            flex-wrap: wrap;
            align-items: baseline;
        }

        .stat {
            display: flex;
            flex-direction: column;
        }

        .stat-value {
            font-size: 3rem;
            font-weight: 700;
            line-height: 1;
            font-variant-numeric: tabular-nums;
        }

        .stat-value.hero-value {
            font-size: 4.5rem;
            color: #4ade80;
        }

        .stat-label {
            font-size: 0.85rem;
            color: #9ca3af;
            margin-top: 0.3rem;
            text-transform: uppercase;
            letter-spacing: 0.05em;
        }

        .hero-nav {
            margin-top: 1.2rem;
            padding-top: 1rem;
            border-top: 1px solid rgba(255,255,255,0.1);
            font-size: 0.85rem;
            color: #6b7280;
        }

        .hero-nav a {
            color: #9ca3af;
            text-decoration: none;
            margin: 0 0.3rem;
        }

        .hero-nav a:hover {
            color: #fff;
        }

        /* ── Cards grid ────────────────────────────────────────── */

        .cards {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
            gap: 16px;
            padding: 20px;
        }

        .card {
            width: 100%;
            height: 120px;
            background-color: #f8f9fa;
            border: 1px solid #ddd;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0, 0, 0, 0.06);
            display: flex;
            flex-direction: column;
            align-items: center;
            justify-content: center;
            text-align: center;
            padding: 10px;
            position: relative;
            overflow: hidden;
            opacity: 0;
            transition: opacity 0.15s ease;
        }

        .card span { font-weight: bold; font-size: 0.95rem; color: #495057; }
        .card small {
            font-style: italic;
            font-size: 0.75rem;
            color: #6c757d;
            margin-top: 6px;
            display: block;
            width: 100%;
            max-width: 140px;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }

        .loading-indicator {
            position: absolute;
            top: 0;
            left: 0;
            width: 100%;
            height: 3px;
            background: linear-gradient(90deg, #f0f0f0, #d4d4d4, #f0f0f0);
            background-size: 200% 100%;
            animation: loading 1s infinite;
            transition: opacity 0.2s ease;
        }

        .load-time-label {
            position: absolute;
            bottom: 4px;
            right: 8px;
            font-size: 0.7rem;
            color: rgba(0, 0, 0, 0.4);
            background-color: rgba(255, 255, 255, 0.7);
            padding: 1px 4px;
            border-radius: 3px;
        }

        @keyframes loading {
            0% { background-position: 200% 0; }
            100% { background-position: -200% 0; }
        }

        /* Tooltip */
        .card::after {
            content: attr(data-performance);
            position: absolute;
            top: -40px;
            left: 50%;
            transform: translateX(-50%);
            background-color: rgba(51, 51, 51, 0.9);
            color: white;
            padding: 5px 10px;
            border-radius: 4px;
            font-size: 0.75rem;
            white-space: nowrap;
            opacity: 0;
            visibility: hidden;
            transition: opacity 0.2s ease, visibility 0.2s ease;
            z-index: 100;
            pointer-events: none;
            box-shadow: 0 2px 5px rgba(0, 0, 0, 0.2);
        }
        .card::before {
            content: '';
            position: absolute;
            top: -10px;
            left: 50%;
            transform: translateX(-50%);
            border-width: 5px;
            border-style: solid;
            border-color: rgba(51, 51, 51, 0.9) transparent transparent transparent;
            opacity: 0;
            visibility: hidden;
            transition: opacity 0.2s ease, visibility 0.2s ease;
            z-index: 100;
            pointer-events: none;
        }
        .card:hover::after,
        .card:hover::before {
            opacity: 1;
            visibility: visible;
        }
    </style>
</head>
<body>

<div class="hero">
    <h1><strong>FrankenAsync</strong> &mdash; <?= $mode ?></h1>
    <div class="stats">
        <div class="stat">
            <span class="stat-value hero-value" id="speed-increase">...</span>
            <span class="stat-label">speedup</span>
        </div>
        <div class="stat">
            <span class="stat-value" id="total-cards">0</span>
            <span class="stat-label">tasks</span>
        </div>
        <div class="stat">
            <span class="stat-value" id="total-request-time">...</span>
            <span class="stat-label">wall clock (ms)</span>
        </div>
        <div class="stat">
            <span class="stat-value" id="total-script-time">...</span>
            <span class="stat-label">aggregated IO (s)</span>
        </div>
    </div>
    <div class="hero-nav">
        <a href="?n=<?= $total ?>&threads=<?= $threads ?>&local=<?= $local ? 0 : 1 ?><?= $coroutines ? '' : '&coroutines=0' ?>"><?= $local ? 'local mock' : 'external API' ?> &#x21C4;</a>
        &middot; <a href="?n=100&threads=10&local=<?= $local ?>">coroutines</a>
        &middot; <a href="?n=100&coroutines=0&local=<?= $local ?>">blocking</a>
        &middot; <a href="?n=500&threads=10&local=<?= $local ?>">500 tasks</a>
        &middot; <a href="?n=50&threads=1&local=<?= $local ?>">1 thread / 50 coroutines</a>
        &middot; <a href="?n=50&coroutines=0&local=<?= $local ?>">50 threads / blocking</a>
    </div>
</div>

<div class="cards">
<?php
    foreach ($comments as $comment) {
        $id = (int)$comment['id'];
        $name = htmlspecialchars($comment['name'] ?? 'Unknown', ENT_QUOTES, 'UTF-8');
        $loadTimeMs = $comment['loadTime'] ?? 0;

        echo <<<HTML
<div class="card" data-load-time="{$loadTimeMs}" data-performance="Task #{$id} - {$loadTimeMs}ms">
    <span><strong>{$id}</strong></span>
    <small>{$name}</small>
</div>
HTML;
    }
?>
</div>

<script>
document.addEventListener('DOMContentLoaded', () => {
    const cards = document.querySelectorAll('.card');
    const cardData = [];

    cards.forEach(card => {
        const loadTime = parseFloat(card.getAttribute('data-load-time'));
        if (!isNaN(loadTime)) {
            cardData.push({ element: card, loadTime: loadTime, animated: false });
        }
    });

    if (cardData.length === 0) return;

    // Sort by load time (fastest first)
    cardData.sort((a, b) => a.loadTime - b.loadTime);
    const minTime = cardData[0].loadTime;
    const maxTime = cardData[cardData.length - 1].loadTime;
    const range = maxTime - minTime;

    // Set initial state — loading indicators
    cardData.forEach(data => {
        const card = data.element;
        card.style.opacity = '0';
        card.style.backgroundColor = '#f8f9fa';
        card.style.borderColor = '#e9ecef';

        const indicator = document.createElement('div');
        indicator.className = 'loading-indicator';
        card.appendChild(indicator);
    });

    // Animation config
    const BATCH_SIZE = 5;
    const BATCH_DELAY = 150;
    const VIEWPORT_ENTRY_DELAY = 300;

    function animateCard(data) {
        if (data.animated) return;
        data.animated = true;

        const card = data.element;
        card.style.transition = 'opacity 0.15s ease';
        card.style.opacity = '1';

        const pos = range > 0 ? (data.loadTime - minTime) / range : 0;
        const color = getColor(pos);

        setTimeout(() => {
            const indicator = card.querySelector('.loading-indicator');
            if (indicator) indicator.remove();

            card.style.transition = 'background-color 0.4s ease';
            card.style.backgroundColor = color;

            if (!card.querySelector('.load-time-label')) {
                const label = document.createElement('div');
                label.className = 'load-time-label';
                label.textContent = data.loadTime.toFixed(0) + 'ms';
                card.appendChild(label);
            }
        }, 100);
    }

    function animateVisibleCards(visibleCards) {
        const toAnimate = visibleCards.filter(d => !d.animated);
        const totalBatches = Math.ceil(toAnimate.length / BATCH_SIZE);

        for (let b = 0; b < totalBatches; b++) {
            const batch = toAnimate.slice(b * BATCH_SIZE, (b + 1) * BATCH_SIZE);
            setTimeout(() => batch.forEach(d => animateCard(d)), VIEWPORT_ENTRY_DELAY + b * BATCH_DELAY);
        }
    }

    // Intersection Observer for viewport-based animation
    const visibleCardsByContainer = new Map();
    const observer = new IntersectionObserver((entries) => {
        entries.forEach(entry => {
            const card = entry.target;
            const data = cardData.find(d => d.element === card);
            if (!data) return;

            const container = card.closest('.cards') || document.body;
            if (!visibleCardsByContainer.has(container)) {
                visibleCardsByContainer.set(container, []);
            }
            const visible = visibleCardsByContainer.get(container);

            if (entry.isIntersecting) {
                if (!visible.includes(data)) visible.push(data);
            } else {
                const idx = visible.indexOf(data);
                if (idx > -1) visible.splice(idx, 1);
            }
        });

        visibleCardsByContainer.forEach(visible => {
            if (visible.length > 0) animateVisibleCards(visible);
        });
    }, { root: null, rootMargin: '0px', threshold: 0.1 });

    cardData.forEach(d => observer.observe(d.element));

    // Trigger initial viewport check
    setTimeout(() => {
        cardData.forEach(data => {
            const rect = data.element.getBoundingClientRect();
            const isVisible = rect.top >= 0 && rect.left >= 0 &&
                rect.bottom <= (window.innerHeight || document.documentElement.clientHeight) &&
                rect.right <= (window.innerWidth || document.documentElement.clientWidth);
            if (isVisible) {
                const container = data.element.closest('.cards') || document.body;
                if (!visibleCardsByContainer.has(container)) visibleCardsByContainer.set(container, []);
                const visible = visibleCardsByContainer.get(container);
                if (!visible.includes(data)) visible.push(data);
            }
        });
        visibleCardsByContainer.forEach(visible => {
            if (visible.length > 0) animateVisibleCards(visible);
        });
    }, 100);

    // Performance metrics
    const totalCards = cards.length;
    document.getElementById('total-cards').textContent = totalCards;

    const wallClock = performance.now();
    document.getElementById('total-request-time').textContent = wallClock.toFixed(0);

    let totalIO = 0;
    cards.forEach(el => {
        const t = parseFloat(el.getAttribute('data-load-time'));
        if (!isNaN(t)) totalIO += t;
    });
    const totalIOSecs = totalIO / 1000;
    document.getElementById('total-script-time').textContent = totalIOSecs.toFixed(1);

    if (totalIOSecs > 0) {
        const speedup = (totalIOSecs / (wallClock / 1000)).toFixed(0);
        document.getElementById('speed-increase').textContent = speedup + 'x';
    }
});

function getColor(pos) {
    pos = Math.max(0, Math.min(1, pos));
    let r, g, b;
    if (pos < 0.5) {
        const f = pos * 2;
        r = Math.round(212 + (255 - 212) * f);
        g = Math.round(237 + (238 - 237) * f);
        b = Math.round(218 + (186 - 218) * f);
    } else {
        const f = (pos - 0.5) * 2;
        r = Math.round(255 + (248 - 255) * f);
        g = Math.round(238 + (215 - 238) * f);
        b = Math.round(186 + (218 - 186) * f);
    }
    return `rgb(${r}, ${g}, ${b})`;
}
</script>

</body>
</html>
