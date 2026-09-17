"""Thin HTTP wrapper around a persistent Camoufox profile.

Endpoints:
  POST /fetch            run fetch() inside an instagram.com page context
  POST /session/import   replace cookies from a Netscape cookie file
  GET  /session/status   cookie health, last error, captured request identity
  GET  /health           process liveness

One browser, one persistent profile on /data/profile. No CDP port.
Request identity (x-asbd-id, graphql doc_id, www-claim) is captured at
runtime from instagram's own in-page requests, never hardcoded.
"""

import asyncio
import os
import time
from pathlib import Path
from urllib.parse import urlparse

from camoufox.async_api import AsyncCamoufox
from fastapi import FastAPI, HTTPException, Request
from pydantic import BaseModel

PROFILE_DIR = os.environ.get("IG_PROFILE_DIR", "/data/profile")
SNAPSHOT_PATH = os.environ.get("IG_SNAPSHOT_PATH", "/data/profile-snapshot.json")
PAGE_TIMEOUT_MS = int(os.environ.get("IG_PAGE_TIMEOUT_MS", "45000"))
HARVEST_TIMEOUT_S = float(os.environ.get("IG_HARVEST_TIMEOUT_S", "8"))

# init script: wrap fetch in the page and record the identity instagram's own code sends on the profile-posts graphql operation.
CAPTURE_JS = """
window.__igcap = null;
const _of = window.fetch;
window.fetch = async function(input, init) {
    try {
        const url = typeof input === "string" ? input : (input && input.url) || "";
        if (url.includes("/graphql/query") && init && typeof init.body === "string"
            && init.body.includes("PolarisProfilePostsTabContentQuery_connection")) {
            const hdrs = {};
            const h = init.headers || {};
            if (h instanceof Headers) { h.forEach((v, k) => hdrs[k] = v); }
            else { Object.assign(hdrs, h); }
            const resp = await _of.apply(this, arguments);
            window.__igcap = {
                headers: hdrs,
                body: init.body,
                www_claim: resp.headers.get("x-ig-set-www-claim") || null,
            };
            return resp;
        }
    } catch (e) {}
    return _of.apply(this, arguments);
};
"""

app = FastAPI()
state = {
    "context": None,
    "page": None,
    "lock": asyncio.Lock(),
    "last_error": None,
    "last_snapshot": 0.0,
    "ready": False,
    "asbd_id": None,
    "doc_id": None,
    "www_claim": None,
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


async def snapshot() -> None:
    ctx = state["context"]
    if ctx is None:
        return
    await ctx.storage_state(path=SNAPSHOT_PATH)
    state["last_snapshot"] = time.time()


async def harvest() -> None:
    """Wait until instagram's own page JS fires the profile-posts graphql
    request, then take the request identity from it."""
    page = state["page"]
    if page is None:
        return
    deadline = time.time() + HARVEST_TIMEOUT_S
    while time.time() < deadline:
        cap = await page.evaluate("() => window.__igcap")
        if cap:
            hdrs = cap.get("headers") or {}
            state["asbd_id"] = hdrs.get("X-ASBD-ID") or hdrs.get("x-asbd-id") or state["asbd_id"]
            state["www_claim"] = cap.get("www_claim") or state["www_claim"]
            body = cap.get("body") or ""
            for piece in body.split("&"):
                if piece.startswith("doc_id="):
                    state["doc_id"] = piece[len("doc_id="):]
            return
        await asyncio.sleep(0.5)


async def navigate(url: str) -> None:
    page = state["page"]
    await page.evaluate("() => { window.__igcap = null; }")
    await page.goto(url, wait_until="domcontentloaded", timeout=PAGE_TIMEOUT_MS)


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
    await page.add_init_script(CAPTURE_JS)
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
           "last_snapshot": state["last_snapshot"],
           "doc_id": state["doc_id"], "asbd_id": state["asbd_id"],
           "www_claim": state["www_claim"]}
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
        await navigate("https://www.instagram.com/")
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
            if req.navigate:
                if not is_instagram(req.navigate):
                    raise HTTPException(400, "only instagram.com urls are allowed")
                await navigate(req.navigate)
                await harvest()
            result = await page.evaluate(
                """async ({url, method, body, asbd, claim}) => {
                    const csrf = document.cookie.split("; ").find(c => c.startsWith("csrftoken="))?.split("=")[1] || "";
                    const headers = {};
                    if (body) {
                        headers["Content-Type"] = "application/x-www-form-urlencoded";
                        headers["X-CSRFToken"] = csrf;
                        headers["X-IG-App-ID"] = "936619743392459";
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
                {"url": req.url, "method": req.method, "body": req.body,
                 "asbd": state["asbd_id"], "claim": state["www_claim"]},
            )
            if result.get("www_claim"):
                state["www_claim"] = result["www_claim"]
            state["last_error"] = None
        except HTTPException:
            raise
        except Exception as e:  # noqa: BLE001 - report everything to the caller
            state["last_error"] = str(e)
            raise HTTPException(502, f"page fetch failed: {e}")
    if result.get("status") == 200:
        await snapshot()
    return result
