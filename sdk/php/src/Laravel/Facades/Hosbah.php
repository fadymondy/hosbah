<?php

declare(strict_types=1);

namespace Hosbah\Laravel\Facades;

use Illuminate\Support\Facades\Facade;

/**
 * @method static string call(string $tool, array $args = [])
 * @method static array tools()
 * @method static string deploy(string $app, bool $wait = false, array $args = [])
 * @method static string setEnv(string $app, string $name, string $value)
 * @method static string addDomain(string $app, string $hostname, array $args = [])
 * @method static string addProxyHost(string $server, string $hostname, string $upstream, array $args = [])
 * @method static string connectServer(string $name, string $host, array $login)
 */
final class Hosbah extends Facade
{
    protected static function getFacadeAccessor(): string
    {
        return \Hosbah\Hosbah::class;
    }
}
