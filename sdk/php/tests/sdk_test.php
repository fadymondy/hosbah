<?php
// Run: php tests/sdk_test.php   (no PHPUnit needed)
declare(strict_types=1);
require __DIR__ . '/../src/Cloudy.php';

use Cloudy\{ApiError, Cloudy, ToolError};

$transport = function (string $url, array $headers, string $body): array {
    assert($url === 'https://c.test/api/mcp');
    if (!in_array('Authorization: Bearer cld_ok', $headers, true)) {
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

$c = new Cloudy('https://c.test/', 'cld_ok', $transport);
$check($c->deploy('shop', wait: true) === 'Deployment #3 succeeded.', 'deploy');
try { $c->deploy('nope'); $check(false, 'tool error'); } catch (ToolError $e) { $check(str_contains($e->getMessage(), 'app not found'), 'tool error'); }
try { (new Cloudy('https://c.test', 'cld_bad', $transport))->apps(); $check(false, 'api error'); } catch (ApiError $e) { $check($e->status === 401, 'api error'); }
try { new Cloudy('https://c.test', ''); $check(false, 'no token'); } catch (InvalidArgumentException) { $check(true, 'no token'); }
exit($fail ? 1 : 0);
