<?php
/**
 * Coroutine-powered batch subrequest.
 *
 * Receives a batch of comment IDs via APP_BATCH_IDS and runs each as a
 * Swow coroutine within this single FrankenPHP thread. This is the
 * "Level 2" concurrency multiplier: each thread handles M coroutines.
 */

use function Frankenphp\async;
use function Frankenphp\await;

$batchIds = json_decode($_SERVER["APP_BATCH_IDS"] ?? "[]", true);
$local = (bool)($_SERVER["APP_LOCAL"] ?? false);

$names = [
    'id labore ex et quam laborum',
    'quo vero reiciendis velit similique earum',
    'odio adipisci rerum aut animi',
    'alias odio sit',
    'vero eaque aliquid doloribus et culpa',
    'et fugit eligendi deleniti quidem qui sint nihil autem',
    'repellat consequatur praesentium vel minus',
    'et omnis dolorem',
    'provident id voluptas',
    'eaque et deleniti atque tenetur ut quo ut',
];

// Resolve API base URL (local Go endpoint on same server)
$port = $_SERVER['SERVER_PORT'] ?? '8081';
$apiBase = "http://127.0.0.1:{$port}/api/comments";

$fetchComment = function($commentId) use ($local, $names, $apiBase) {
    $start = microtime(true);

    if ($local) {
        usleep(random_int(100000, 500000));
        $name = $names[($commentId - 1) % count($names)];
    } else {
        $data = @file_get_contents("{$apiBase}/{$commentId}");
        if ($data === false) {
            $err = error_get_last();
            throw new \RuntimeException("HTTP fetch failed for comment {$commentId}: " . ($err['message'] ?? 'unknown error'));
        }
        $comment = json_decode($data, true);
        $name = $comment['name'] ?? 'Unknown';
    }

    $loadTimeMs = round((microtime(true) - $start) * 1000, 2);
    return ['id' => $commentId, 'name' => $name, 'loadTime' => $loadTimeMs];
};

// Local mock: fire all at once (usleep is cheap, no real sockets)
// HTTP mode: limit concurrent sockets to avoid Swow multi-thread contention
$concurrency = $local ? count($batchIds) : 10;

$results = [];
foreach (array_chunk($batchIds, $concurrency) as $chunk) {
    $tasks = [];
    foreach ($chunk as $commentId) {
        $tasks[] = async(fn() => $fetchComment($commentId));
    }
    $chunkResults = await($tasks, "30s");
    if (!is_array($chunkResults) || isset($chunkResults['id'])) {
        $chunkResults = [$chunkResults];
    }
    $results = array_merge($results, $chunkResults);
}

header('Content-Type: application/json');
echo json_encode($results);
