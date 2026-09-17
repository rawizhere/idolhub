# syntax=docker/dockerfile:1

FROM python:3.13-slim

# Xvfb for virtual-headless mode, GTK/X/ALSA libs for the firefox binary.
RUN apt-get update && apt-get install -y --no-install-recommends \
    xvfb xauth \
    libgtk-3-0 libdbus-glib-1-2 libxt6 libasound2 \
    libx11-xcb1 libxcomposite1 libxdamage1 libxrandr2 libxkbcommon0 \
    libpango-1.0-0 libcairo2 libatk1.0-0 libatk-bridge2.0-0 \
    libcups2 libdrm2 libgbm1 libxfixes3 libnss3 \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir camoufox[geoip] fastapi uvicorn

# Download the browser binary at build time, not on first start.
RUN python -m camoufox fetch

WORKDIR /app
COPY app.py .

EXPOSE 9100
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "9100"]
