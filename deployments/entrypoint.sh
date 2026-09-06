#!/bin/sh
set -e

if [ "$(id -u)" = "0" ]; then
  if [ -d /app/downloads ]; then
    find /app/downloads -type f -name posts.json -exec mv {} {}.bak \;
  fi
  mkdir -p /app/configs
  chown idolhub:idolhub /app/configs
  exec su-exec idolhub "$@"
fi

exec "$@"
