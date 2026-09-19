# fadymondy/hosbah-sdk (PHP + Laravel)

```bash
composer require fadymondy/hosbah-sdk
```

```php
// Laravel: set CLOUDY_URL and CLOUDY_TOKEN in .env — the provider and facade are auto-discovered.
use Hosbah\Laravel\Facades\Hosbah;

Hosbah::deploy('shop', wait: true);
Hosbah::addProxyHost('edge', 'nas.example.com', '10.0.0.5:5000');
Hosbah::call('list_databases');          // any tool — see Hosbah::tools()

// Plain PHP
$hosbah = new \Hosbah\Hosbah(getenv('CLOUDY_URL'), getenv('CLOUDY_TOKEN'));
```

The token is a team MCP token (`cld_…`); its scopes decide what the SDK may do. Publish the config with
`php artisan vendor:publish --tag=hosbah-config`. Tests: `php tests/sdk_test.php`.
