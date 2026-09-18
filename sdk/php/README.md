# fadymondy/cloudy-sdk (PHP + Laravel)

```bash
composer require fadymondy/cloudy-sdk
```

```php
// Laravel: set CLOUDY_URL and CLOUDY_TOKEN in .env — the provider and facade are auto-discovered.
use Cloudy\Laravel\Facades\Cloudy;

Cloudy::deploy('shop', wait: true);
Cloudy::addProxyHost('edge', 'nas.example.com', '10.0.0.5:5000');
Cloudy::call('list_databases');          // any tool — see Cloudy::tools()

// Plain PHP
$cloudy = new \Cloudy\Cloudy(getenv('CLOUDY_URL'), getenv('CLOUDY_TOKEN'));
```

The token is a team MCP token (`cld_…`); its scopes decide what the SDK may do. Publish the config with
`php artisan vendor:publish --tag=cloudy-config`. Tests: `php tests/sdk_test.php`.
