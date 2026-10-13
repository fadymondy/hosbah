#!/usr/bin/env sh
# Run every flow against a device with the app installed and Metro in mock mode (npm run mock).
set -e
cd "$(dirname "$0")"
maestro test sign-in.yaml
maestro test apps-actions.yaml
maestro test tabs.yaml
