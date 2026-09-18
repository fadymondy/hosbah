<?php

declare(strict_types=1);

namespace Cloudy;

/**
 * Cloudy SDK for PHP 8.1+ (framework-free; Laravel integration in Cloudy\Laravel).
 *
 * It calls a team's MCP endpoint (POST /api/mcp) with a team token (cld_…), so every Cloudy tool is
 * available through call(), with the token's scopes, the audit log and destructive-action approvals.
 *
 *   $cloudy = new \Cloudy\Cloudy(getenv('CLOUDY_URL'), getenv('CLOUDY_TOKEN'));
 *   $cloudy->deploy('shop', wait: true);
 */
final class Cloudy
{
    private string $url;
    private string $token;
    private int $id = 0;
    /** @var callable(string $url, array $headers, string $body): array{0:int,1:string} */
    private $transport;

    /**
     * @param callable|null $transport fn(string $url, array $headers, string $body): [int $status, string $body]
     */
    public function __construct(string $url, string $token, ?callable $transport = null, private int $timeout = 600)
    {
        $this->url = rtrim($url, '/');
        $this->token = trim($token);
        if ($this->token === '') {
            throw new \InvalidArgumentException('cloudy: no token (mint one under MCP tokens)');
        }
        $this->transport = $transport ?? [$this, 'httpPost'];
    }

    /** @return array<int, array{name:string, description:string, inputSchema:array}> */
    public function tools(): array
    {
        return $this->rpc('tools/list', new \stdClass())['tools'] ?? [];
    }

    /**
     * Run any tool; returns its text output, throws ToolError on a refusal.
     *
     * @param array<string, mixed> $args
     */
    public function call(string $tool, array $args = []): string
    {
        $r = $this->rpc('tools/call', ['name' => $tool, 'arguments' => (object) $args]);
        $text = implode('', array_map(fn ($c) => $c['text'] ?? '', $r['content'] ?? []));
        if (!empty($r['isError'])) {
            throw new ToolError($tool, $text);
        }
        return $text;
    }

    public function whoami(): string { return $this->call('whoami'); }
    public function servers(): string { return $this->call('list_servers'); }
    public function apps(): string { return $this->call('list_apps'); }

    public function deploy(string $app, bool $wait = false, array $args = []): string
    {
        return $this->call('deploy_app', ['app' => $app, 'wait' => $wait] + $args);
    }

    public function setEnv(string $app, string $name, string $value): string
    {
        return $this->call('set_env', ['app' => $app, 'name' => $name, 'value' => $value]);
    }

    public function addDomain(string $app, string $hostname, array $args = []): string
    {
        return $this->call('add_domain', ['app' => $app, 'hostname' => $hostname] + $args);
    }

    public function addProxyHost(string $server, string $hostname, string $upstream, array $args = []): string
    {
        return $this->call('add_proxy_host', ['server' => $server, 'hostname' => $hostname, 'upstream' => $upstream] + $args);
    }

    /** @param array{user?:string,password?:string,private_key?:string,port?:int} $login */
    public function connectServer(string $name, string $host, array $login): string
    {
        return $this->call('add_server', ['name' => $name, 'method' => 'connect', 'host' => $host] + $login);
    }

    private function rpc(string $method, array|object $params): array
    {
        $body = json_encode(['jsonrpc' => '2.0', 'id' => ++$this->id, 'method' => $method, 'params' => $params], JSON_THROW_ON_ERROR);
        [$status, $raw] = ($this->transport)($this->url . '/api/mcp', [
            'Authorization: Bearer ' . $this->token,
            'Content-Type: application/json',
            'Accept: application/json',
        ], $body);
        $data = json_decode($raw, true);
        if (!is_array($data)) {
            throw new ApiError($status, substr($raw, 0, 200) ?: 'unexpected response');
        }
        if (isset($data['error'])) {
            throw new ApiError($status, (string) ($data['error']['message'] ?? 'error'));
        }
        if ($status >= 300) {
            throw new ApiError($status, 'request failed');
        }
        return $data['result'] ?? [];
    }

    /** @return array{0:int,1:string} */
    private function httpPost(string $url, array $headers, string $body): array
    {
        $ctx = stream_context_create(['http' => [
            'method' => 'POST',
            'header' => implode("\r\n", $headers),
            'content' => $body,
            'timeout' => $this->timeout,
            'ignore_errors' => true,
        ]]);
        $raw = @file_get_contents($url, false, $ctx);
        if ($raw === false) {
            throw new ApiError(0, 'could not reach ' . $url);
        }
        $status = 0;
        foreach ($http_response_header ?? [] as $h) {
            if (preg_match('#^HTTP/\S+\s+(\d{3})#', $h, $m)) {
                $status = (int) $m[1];
            }
        }
        return [$status, $raw];
    }
}

/** A tool refused (not found, missing scope, validation…). */
final class ToolError extends \RuntimeException
{
    public function __construct(public readonly string $tool, string $message)
    {
        parent::__construct("cloudy: {$tool}: {$message}");
    }
}

/** Transport or protocol failure (bad token, server error). */
final class ApiError extends \RuntimeException
{
    public function __construct(public readonly int $status, string $message)
    {
        parent::__construct("cloudy: {$message} (HTTP {$status})");
    }
}
