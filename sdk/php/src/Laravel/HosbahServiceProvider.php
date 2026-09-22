<?php

declare(strict_types=1);

namespace Hosbah\Laravel;

use Hosbah\Hosbah;
use Illuminate\Support\ServiceProvider;

/**
 * Binds Hosbah\Hosbah from config/hosbah.php (HOSBAH_URL, HOSBAH_TOKEN). Auto-discovered.
 * An app that published a config/cloudy.php before the rename keeps working: its values are read
 * when config/hosbah.php has none.
 */
final class HosbahServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__ . '/../../config/hosbah.php', 'hosbah');
        $this->app->singleton(Hosbah::class, function ($app) {
            $config = $app['config'];
            $get = fn (string $key, $default = null) => $config->get('hosbah.' . $key) ?? $config->get('cloudy.' . $key, $default);

            return new Hosbah(
                (string) $get('url'),
                (string) $get('token'),
                null,
                (int) $get('timeout', 600),
            );
        });
        $this->app->alias(Hosbah::class, 'hosbah');
    }

    public function boot(): void
    {
        $this->publishes([__DIR__ . '/../../config/hosbah.php' => config_path('hosbah.php')], 'hosbah-config');
    }
}
