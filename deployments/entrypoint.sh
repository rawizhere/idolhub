#!/bin/sh
set -e

if [ "$(id -u)" = "0" ]; then
  mkdir -p /app/configs
  chown idolhub:idolhub /app/configs
  exec su-exec idolhub "$@"
fi

exec "$@"
