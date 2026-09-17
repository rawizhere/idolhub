# syntax=docker/dockerfile:1

FROM python:3.13-slim

# Camoufox needs Xvfb for its virtual-headless mode.
RUN apt-get update && apt-get install -y --no-install-recommends \
    xvfb xauth \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir camoufox[geoip] fastapi uvicorn

# Download the browser binary at build time, not on first start.
RUN python -m camoufox fetch

WORKDIR /app
COPY app.py .

EXPOSE 9100
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "9100"]
