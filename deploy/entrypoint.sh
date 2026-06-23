#!/bin/sh
set -e

if [ "$(id -u)" = "0" ]; then
  mkdir -p /app/configs
  chown idolhub:idolhub /app/configs
  if [ -d /app/configs/config.json ]; then
    rm -rf /app/configs/config.json
  fi
  exec su-exec idolhub "$@"
fi

exec "$@"
