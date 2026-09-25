<?php
// Run: php tests/sdk_test.php   (no PHPUnit needed)
declare(strict_types=1);
require __DIR__ . '/../src/Hosbah.php';

use Hosbah\{ApiError, Hosbah, ToolError};

$transport = function (string $url, array $headers, string $body): array {
    assert($url === 'https://c.test/api/mcp');
    if (!in_array('Authorization: Bearer hbu_ok', $headers, true)) {
        return [401, '{"error":{"message":"invalid token"}}'];
    }
    $req = json_decode($body, true);
    $args = $req['params']['arguments'] ?? [];
    if (($req['params']['name'] ?? '') === 'deploy_app' && ($args['app'] ?? '') === 'shop' && ($args['wait'] ?? false) === true) {
        return [200, '{"result":{"content":[{"type":"text","text":"Deployment #3 succeeded."}]}}'];
    }
    return [200, '{"result":{"content":[{"type":"text","text":"app not found"}],"isError":true}}'];
};

$fail = 0;
$check = function (bool $ok, string $what) use (&$fail) { echo ($ok ? 'ok   ' : 'FAIL ') . $what . PHP_EOL; $fail += $ok ? 0 : 1; };

$c = new Hosbah('https://c.test/', 'hbu_ok', $transport);
$check($c->deploy('shop', wait: true) === 'Deployment #3 succeeded.', 'deploy');
try { $c->deploy('nope'); $check(false, 'tool error'); } catch (ToolError $e) { $check(str_contains($e->getMessage(), 'app not found'), 'tool error'); }
try { (new Hosbah('https://c.test', 'hbu_bad', $transport))->apps(); $check(false, 'api error'); } catch (ApiError $e) { $check($e->status === 401, 'api error'); }
try { new Hosbah('https://c.test', ''); $check(false, 'no token'); } catch (InvalidArgumentException) { $check(true, 'no token'); }

// A wait longer than one request: follow each continue: line until the deployment has finished.
$calls = 0;
$chain = function (string $url, array $headers, string $body) use (&$calls): array {
    $calls++;
    $name = json_decode($body, true)['params']['name'];
    $next = 'continue: wait_deployment {"app":"shop","deployment":"4","until":"2026-09-25T12:00:00Z"}' . "\n";
    $text = match (true) {
        $name === 'deploy_app' => "Deploy #4 of shop queued.\n" . $next,
        $calls === 2 => "#4 is running.\n" . $next,
        default => "✓ #4 succeeded in 1m2s.\n",
    };
    return [200, json_encode(['result' => ['content' => [['type' => 'text', 'text' => $text]]]])];
};
$out = (new Hosbah('https://c.test', 'hbu_ok', $chain))->deploy('shop', wait: true);
$check($out === "Deploy #4 of shop queued.\n✓ #4 succeeded in 1m2s.\n" && $calls === 3, 'follows continue');
exit($fail ? 1 : 0);
