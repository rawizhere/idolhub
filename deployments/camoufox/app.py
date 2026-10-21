"""Thin HTTP wrapper around a persistent Camoufox profile.

Endpoints:
  POST /fetch            run fetch() inside an instagram.com page context
  POST /session/import   replace cookies from a Netscape cookie file
  GET  /session/status   cookie health, last error, captured request identity

One browser, one persistent profile on /data/profile. No CDP port.
Request identity (x-asbd-id, graphql doc_id, fb_dtsg, www-claim) is captured
at runtime from the browser network events of instagram's own requests,
never hardcoded.
"""

import asyncio
import os
import re
import time
from pathlib import Path
from urllib.parse import parse_qsl, quote_plus, urlparse

from camoufox.async_api import AsyncCamoufox
from fastapi import FastAPI, HTTPException, Request
from pydantic import BaseModel

PROFILE_DIR = "/data/profile"
PAGE_TIMEOUT_MS = 45000
HARVEST_TIMEOUT_S = 8

app = FastAPI()
state = {
    "context": None,
    "page": None,
    "lock": asyncio.Lock(),
    "last_error": None,
    "ready": False,
    "asbd_id": None,
    "doc_id": None,
    "www_claim": None,
    "fb_dtsg": None,
}


class FetchReq(BaseModel):
    url: str
    method: str = "GET"
    body: str | None = None
    navigate: str | None = None


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
    host = urlparse(url).hostname or ""
    return host == "instagram.com" or host.endswith(".instagram.com")


async def capture_request(req) -> None:
    """Page-network level capture: instagram's own code does not always go
    through the wrapped window.fetch, so record the profile-posts graphql
    request straight from the browser network events."""
    try:
        if "/api/graphql" not in req.url and "/graphql/query" not in req.url:
            return
        post = req.post_data
        if isinstance(post, bytes):
            post = post.decode("utf-8", "replace")
        if not post or "PolarisProfilePostsQuery" not in post or "__dyn=" not in post:
            return
        hdrs = req.headers
        parsed = dict(parse_qsl(post))
        if parsed.get("doc_id"):
            state["doc_id"] = parsed["doc_id"]
        if parsed.get("fb_dtsg"):
            state["fb_dtsg"] = parsed["fb_dtsg"]
        if hdrs.get("x-asbd-id") or hdrs.get("X-ASBD-ID"):
            state["asbd_id"] = hdrs.get("x-asbd-id") or hdrs.get("X-ASBD-ID")
    except Exception as e:  # noqa: BLE001 - capture must never break the request flow
        state["capture_error"] = str(e)


async def capture_response(resp) -> None:
    try:
        if "/api/graphql" not in resp.url and "/graphql/query" not in resp.url:
            return
        claim = resp.headers.get("x-ig-set-www-claim")
        if claim:
            state["www_claim"] = claim
    except Exception:  # noqa: BLE001
        pass


async def harvest() -> None:
    """Wait until instagram's own page JS fires the profile-posts graphql
    request, then take the request identity from it."""
    if state["page"] is None:
        return
    deadline = time.time() + HARVEST_TIMEOUT_S
    while time.time() < deadline:
        if state["doc_id"] and state["fb_dtsg"]:
            return
        await asyncio.sleep(0.5)


async def navigate(url: str) -> None:
    await state["page"].goto(url, wait_until="domcontentloaded", timeout=PAGE_TIMEOUT_MS)


# UA must equal the one the instagram sessionid was minted with, else instagram returns "useragent mismatch"
IG_USER_AGENT = os.environ.get("IG_USER_AGENT", "").strip()


async def launch_browser() -> None:
    Path(PROFILE_DIR).mkdir(parents=True, exist_ok=True)
    opts = dict(
        persistent_context=True,
        user_data_dir=PROFILE_DIR,
        headless="virtual",
        i_know_what_im_doing=True,
    )
    if IG_USER_AGENT:
        # camoufox derives the rest of the fingerprint (os, screen, fonts) from a custom UA
        opts["config"] = {"navigator.userAgent": IG_USER_AGENT,
                          "headers.User-Agent": IG_USER_AGENT}
    launch = AsyncCamoufox(**opts)
    state["context"] = await launch.__aenter__()
    page = await state["context"].new_page()
    page.on("request", lambda r: asyncio.ensure_future(capture_request(r)))
    page.on("response", lambda r: asyncio.ensure_future(capture_response(r)))
    await page.goto("https://www.instagram.com/", wait_until="domcontentloaded",
                    timeout=PAGE_TIMEOUT_MS)
    state["page"] = page
    state["last_error"] = None
    state["ready"] = True


async def ensure_browser() -> None:
    """Relaunch the browser if it died (oom kill, crash); the container keeps serving."""
    page = state["page"]
    context = state["context"]
    if page is not None and not page.is_closed() and context is not None:
        try:
            # bounded probe: a wedged page must fail here and trigger relaunch, never hang the caller
            await asyncio.wait_for(page.evaluate("() => 1"), timeout=10)
            return
        except Exception:
            pass
    # caller already holds state["lock"]; re-acquiring it here deadlocked every /fetch forever
    state["last_error"] = "browser died; relaunching"
    try:
        if context is not None:
            await context.close()
    except Exception:
        pass
    state["page"] = None
    state["context"] = None
    state["ready"] = False
    await launch_browser()


@app.on_event("startup")
async def startup() -> None:
    await launch_browser()


@app.get("/session/status")
async def status() -> dict:
    ctx = state["context"]
    out = {"ready": state["ready"], "last_error": state["last_error"],
           "doc_id": state["doc_id"], "asbd_id": state["asbd_id"],
           "www_claim": state["www_claim"], "fb_dtsg": state["fb_dtsg"],
           "capture_error": state.get("capture_error")}
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
        await ensure_browser()
        await state["context"].clear_cookies()
        await state["context"].add_cookies(cookies)
        await navigate("https://www.instagram.com/")
    return {"imported": len(cookies), "status": "ok"}


@app.post("/fetch")
async def fetch(req: FetchReq) -> dict:
    if not is_instagram(req.url):
        raise HTTPException(400, "only instagram.com urls are allowed")
    page = state["page"]
    if page is None:
        raise HTTPException(503, "browser not ready")
    async with state["lock"]:
        await ensure_browser()
        try:
            if req.navigate:
                if not is_instagram(req.navigate):
                    raise HTTPException(400, "only instagram.com urls are allowed")
                await navigate(req.navigate)
                await harvest()
            # the body was built before the navigation that harvested the fresh identity; substitute it
            body = req.body
            if body:
                if state["fb_dtsg"] and "fb_dtsg=" in body:
                    body = re.sub(r"fb_dtsg=[^&]*", "fb_dtsg=" + quote_plus(state["fb_dtsg"]), body)
                if state["doc_id"] and "doc_id=" in body:
                    body = re.sub(r"doc_id=[^&]*", "doc_id=" + state["doc_id"], body)
            # bound the whole in-page fetch so a stuck page returns 502 and releases the lock
            result = await asyncio.wait_for(page.evaluate(
                """async ({url, method, body, asbd, claim}) => {
                    const csrf = document.cookie.split("; ").find(c => c.startsWith("csrftoken="))?.split("=")[1] || "";
                    const headers = {};
                    headers["X-IG-App-ID"] = "936619743392459";
                    if (body) {
                        headers["Content-Type"] = "application/x-www-form-urlencoded";
                        headers["X-CSRFToken"] = csrf;
                        headers["X-Requested-With"] = "XMLHttpRequest";
                    }
                    if (asbd) { headers["X-ASBD-ID"] = asbd; }
                    if (claim) { headers["X-IG-WWW-Claim"] = claim; }
                    const r = await fetch(url, {
                        method: method,
                        credentials: "include",
                        headers: headers,
                        body: body || undefined,
                    });
                    const text = await r.text();
                    return {status: r.status, body: text,
                            www_claim: r.headers.get("x-ig-set-www-claim")};
                }""",
                {"url": req.url, "method": req.method, "body": body,
                 "asbd": state["asbd_id"], "claim": state["www_claim"]},
            ), timeout=PAGE_TIMEOUT_MS / 1000 + 5)
            if result.get("www_claim"):
                state["www_claim"] = result["www_claim"]
            state["last_error"] = None
        except HTTPException:
            raise
        except Exception as e:  # noqa: BLE001 - report everything to the caller
            state["last_error"] = str(e)
            raise HTTPException(502, f"page fetch failed: {e}")
    return result
