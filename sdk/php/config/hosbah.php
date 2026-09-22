<?php

// HOSBAH_* first; CLOUDY_*, the names before the rename, are still read when those are not set.
return [
    // The Hosbah origin, e.g. https://app.hosbah.com
    'url' => env('HOSBAH_URL', env('CLOUDY_URL')),
    // A team MCP token (hbu_…, or cld_… minted before the rename) from MCP tokens in the dashboard;
    // its scopes decide what the SDK may do.
    'token' => env('HOSBAH_TOKEN', env('CLOUDY_TOKEN')),
    'timeout' => (int) env('HOSBAH_TIMEOUT', env('CLOUDY_TIMEOUT', 600)),
];
