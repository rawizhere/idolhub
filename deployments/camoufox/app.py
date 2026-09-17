"""Thin HTTP wrapper around a persistent Camoufox profile.

Endpoints:
  POST /fetch            run fetch() inside an instagram.com page context
  POST /session/import   replace cookies from a Netscape cookie file
  GET  /session/status   cookie health and last error
  GET  /health           process liveness

One browser, one persistent profile on /data/profile. No CDP port.
"""

import asyncio
import json
import os
import time
from pathlib import Path

from camoufox.async_api import AsyncCamoufox
from fastapi import FastAPI, HTTPException, Request
from pydantic import BaseModel

PROFILE_DIR = os.environ.get("IG_PROFILE_DIR", "/data/profile")
SNAPSHOT_PATH = os.environ.get("IG_SNAPSHOT_PATH", "/data/profile-snapshot.json")
PAGE_TIMEOUT_MS = int(os.environ.get("IG_PAGE_TIMEOUT_MS", "45000"))

app = FastAPI()
state = {
    "context": None,
    "page": None,
    "lock": asyncio.Lock(),
    "last_error": None,
    "last_snapshot": 0.0,
    "ready": False,
}


class FetchReq(BaseModel):
    url: str
    method: str = "GET"
    body: str | None = None


def parse_netscape(raw: str) -> list[dict]:
    cookies = []
    for line in raw.splitlines():
        http_only = line.startswith("#HttpOnly_")
        if http_only:
            line = line[len("#HttpOnly_"):]
        elif line.startswith("#") or not line.strip():
            continue
        parts = line.split("\t")
        if len(parts) < 7 or "instagram.com" not in parts[0]:
            continue
        cookies.append({
            "name": parts[5],
            "value": "".join(parts[6:]),
            "domain": parts[0],
            "path": parts[2],
            "secure": parts[3] == "TRUE",
            "httpOnly": http_only,
            "expires": int(parts[4]),
        })
    if not cookies:
        raise ValueError("no instagram.com cookies in the file")
    return cookies


def is_instagram(url: str) -> bool:
    from urllib.parse import urlparse
    host = urlparse(url).hostname or ""
    return host == "instagram.com" or host.endswith(".instagram.com")


async def snapshot() -> None:
    ctx = state["context"]
    if ctx is None:
        return
    await ctx.storage_state(path=SNAPSHOT_PATH)
    state["last_snapshot"] = time.time()


@app.on_event("startup")
async def startup() -> None:
    Path(PROFILE_DIR).mkdir(parents=True, exist_ok=True)
    launch = AsyncCamoufox(
        persistent_context=True,
        user_data_dir=PROFILE_DIR,
        headless="virtual",
        i_know_what_im_doing=True,
    )
    state["context"] = await launch.__aenter__()
    page = await state["context"].new_page()
    await page.goto("https://www.instagram.com/", wait_until="domcontentloaded",
                    timeout=PAGE_TIMEOUT_MS)
    state["page"] = page
    state["ready"] = True


@app.get("/health")
async def health() -> dict:
    return {"ready": state["ready"], "last_error": state["last_error"]}


@app.get("/session/status")
async def status() -> dict:
    ctx = state["context"]
    out = {"ready": state["ready"], "last_error": state["last_error"],
           "last_snapshot": state["last_snapshot"]}
    if ctx is None:
        out["session"] = "no-context"
        return out
    cookies = {c["name"]: c for c in await ctx.cookies()}
    sid = cookies.get("sessionid")
    if not sid:
        out["session"] = "expired"
    else:
        out["session"] = "ok"
        out["sessionid_expires"] = sid.get("expires")
        out["cookies"] = len(cookies)
        if ds := cookies.get("ds_user_id"):
            out["ds_user_id"] = ds.get("value")
    return out


@app.post("/session/import")
async def session_import(request: Request) -> dict:
    raw = (await request.body()).decode()
    try:
        cookies = parse_netscape(raw)
    except ValueError as e:
        raise HTTPException(400, str(e))
    async with state["lock"]:
        await state["context"].clear_cookies()
        await state["context"].add_cookies(cookies)
        page = state["page"]
        # reload so the context picks up the new identity
        await page.goto("https://www.instagram.com/", wait_until="domcontentloaded",
                        timeout=PAGE_TIMEOUT_MS)
        await snapshot()
    return {"imported": len(cookies), "status": "ok"}


@app.post("/fetch")
async def fetch(req: FetchReq) -> dict:
    if not is_instagram(req.url):
        raise HTTPException(400, "only instagram.com urls are allowed")
    page = state["page"]
    if page is None:
        raise HTTPException(503, "browser not ready")
    async with state["lock"]:
        try:
            result = await page.evaluate(
                """async ({url, method, body}) => {
                    const r = await fetch(url, {
                        method: method,
                        credentials: "include",
                        headers: body ? {"Content-Type": "application/x-www-form-urlencoded"} : {},
                        body: body || undefined,
                    });
                    const text = await r.text();
                    return {status: r.status, body: text};
                }""",
                {"url": req.url, "method": req.method, "body": req.body},
            )
            state["last_error"] = None
        except Exception as e:  # noqa: BLE001 - report everything to the caller
            state["last_error"] = str(e)
            raise HTTPException(502, f"page fetch failed: {e}")
    if result.get("status") == 200:
        await snapshot()
    return result
