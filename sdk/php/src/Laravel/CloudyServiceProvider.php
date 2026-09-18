<?php

declare(strict_types=1);

namespace Cloudy\Laravel;

use Cloudy\Cloudy;
use Illuminate\Support\ServiceProvider;

/** Binds Cloudy\Cloudy from config/cloudy.php (CLOUDY_URL, CLOUDY_TOKEN). Auto-discovered. */
final class CloudyServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__ . '/../../config/cloudy.php', 'cloudy');
        $this->app->singleton(Cloudy::class, fn ($app) => new Cloudy(
            (string) $app['config']->get('cloudy.url'),
            (string) $app['config']->get('cloudy.token'),
            null,
            (int) $app['config']->get('cloudy.timeout', 600),
        ));
        $this->app->alias(Cloudy::class, 'cloudy');
    }

    public function boot(): void
    {
        $this->publishes([__DIR__ . '/../../config/cloudy.php' => config_path('cloudy.php')], 'cloudy-config');
    }
}
