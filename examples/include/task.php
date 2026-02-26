<?php
/**
 * Single blocking task.
 *
 * Each task runs on its own FrankenPHP thread with blocking I/O.
 */

$commentId = (int)($_SERVER["APP_ID"] ?? 1);
$local = (bool)($_SERVER["APP_LOCAL"] ?? false);

$startTime = microtime(true);

if ($local) {
    // Simulate I/O delay (100-500ms, varying API response times)
    usleep(random_int(100000, 500000));

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

    $name = $names[($commentId - 1) % count($names)];
} else {
    $port = $_SERVER['SERVER_PORT'] ?? '8081';
    $comment = file_get_contents("http://127.0.0.1:{$port}/api/comments/$commentId");
    $commentData = json_decode($comment);
    $name = $commentData->name ?? 'Unknown';
}

$loadTimeMs = round((microtime(true) - $startTime) * 1000, 2);

header('Content-Type: application/json');
echo json_encode([
    'id'       => $commentId,
    'name'     => $name,
    'loadTime' => $loadTimeMs,
]);
