<?php

declare(strict_types=1);

namespace Hosbah\Laravel;

use Hosbah\Hosbah;
use Illuminate\Support\ServiceProvider;

/** Binds Hosbah\Hosbah from config/hosbah.php (CLOUDY_URL, CLOUDY_TOKEN). Auto-discovered. */
final class HosbahServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__ . '/../../config/hosbah.php', 'hosbah');
        $this->app->singleton(Hosbah::class, fn ($app) => new Hosbah(
            (string) $app['config']->get('cloudy.url'),
            (string) $app['config']->get('cloudy.token'),
            null,
            (int) $app['config']->get('cloudy.timeout', 600),
        ));
        $this->app->alias(Hosbah::class, 'hosbah');
    }

    public function boot(): void
    {
        $this->publishes([__DIR__ . '/../../config/hosbah.php' => config_path('hosbah.php')], 'hosbah-config');
    }
}
