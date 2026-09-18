<?php

return [
    // The Cloudy origin, e.g. https://cloudy-app.fadymondy.com
    'url' => env('CLOUDY_URL'),
    // A team MCP token (cld_…) from MCP tokens in the dashboard; its scopes decide what the SDK may do.
    'token' => env('CLOUDY_TOKEN'),
    'timeout' => (int) env('CLOUDY_TIMEOUT', 600),
];
