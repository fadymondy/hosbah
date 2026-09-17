<?php
$version = 'v1';
if (($_SERVER['REQUEST_URI'] ?? '/') === '/health') { echo "ok\n"; return; }
header('Content-Type: text/plain');
echo "hello from PHP $version (" . getenv('GREETING') . ")\n";
