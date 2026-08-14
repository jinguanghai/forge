# -*- coding: utf-8 -*-
"""browser_gate: Forge browser gate (Playwright atomic operations)
Input: {"action":"navigate|get_text|click|type|screenshot|extract|wait|close|status", ...}
Output: {"ok":true,"result":...} or {"ok":false,"error":"..."}
Security: read-only by default; click/type require explicit commands; no downloading executables; headed by default, headless configurable"""
import sys, json, io, os, time
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

try:
    from playwright.sync_api import sync_playwright
except Exception as e:
    print(json.dumps({"ok": False, "error": f"playwright not installed: {e}"}, ensure_ascii=False)); sys.exit(0)

_ctx = {"channel": "msedge"}

_CN_DOMAINS = {".cn", "baidu.com", "qq.com", "163.com", "taobao.com", "tmall.com", "jd.com",
              "zhihu.com", "weibo.com", "douyin.com", "bilibili.com", "sina.com.cn", "sohu.com",
              "360.cn", "xinhuanet.com", "people.com.cn", "csdn.net", "gitee.com", "aliyun.com",
              "tencent.com", "bytedance.com", "meituan.com", "dianping.com", "xueqiu.com"}

def _need_proxy(url):
    from urllib.parse import urlparse
    host = (urlparse(url).hostname or "").lower()
    if not host:
        return False
    if host == "cn" or host.endswith(".cn"):
        return False
    for d in _CN_DOMAINS:
        if host == d or host.endswith("." + d):
            return False
    return True

def _env_proxy():
    return os.environ.get("HTTPS_PROXY") or os.environ.get("HTTP_PROXY") or os.environ.get("ALL_PROXY") or os.environ.get("https_proxy") or os.environ.get("http_proxy")

def _http_bridge_alive():
    try:
        import socket
        s = socket.create_connection(("127.0.0.1", 1081), timeout=0.5); s.close(); return True
    except Exception:
        return False

def _map_proxy(p):
    """Edge does not support socks5h (ERR_NO_SUPPORTED_PROXIES); local bridge (1081) forwards HTTP CONNECT as socks5h (DNS resolved on VPS side)"""
    if p and p.startswith(("socks5://", "socks5h://")) and _http_bridge_alive():
        return "http://127.0.0.1:1081"
    return p

def _ensure(proxy=None):
    if "page" in _ctx and _ctx["page"] is not None and _ctx.get("proxy") == proxy:
        return _ctx["page"]
    _close()
    pw = sync_playwright().start()
    headless = _ctx.get("headless", False)
    kw = {}
    if proxy:
        kw["proxy"] = {"server": _map_proxy(proxy)}  # socks5h passthrough: DNS resolved proxy-side to avoid pollution
    else:
        # direct: must prevent chromium from inheriting system proxy env vars
        for k in ("HTTP_PROXY","HTTPS_PROXY","ALL_PROXY","http_proxy","https_proxy","all_proxy"):
            os.environ.pop(k, None)
        kw["args"] = ["--no-proxy-server"]
    browser = pw.chromium.launch(headless=headless, channel=_ctx.get("channel", "msedge"), **kw)
    page = browser.new_page()
    page.set_default_timeout(20000)
    _ctx.update(pw=pw, browser=browser, page=page, proxy=proxy)
    return page

def _close():
    for k in ("page", "browser", "pw"):
        v = _ctx.pop(k, None)
        try: v.close() if v else None
        except Exception: pass
    return {"ok": True, "result": "closed"}

def handle(req):
    act = req.get("action", "")
    if act == "close":
        return _close()
    page = _ensure()
    try:
        if act == "navigate":
            url = req["url"]
            _ctx["headless"] = bool(req.get("headless", _ctx.get("headless", False)))
            proxy = _env_proxy() if _need_proxy(url) else None
            page = _ensure(proxy)
            if req.get("block_resources", True):
                page.route("**/*", lambda route: route.abort()
                           if route.request.resource_type in ("image", "font", "media")
                           else route.continue_())
            wu = req.get("wait_until", "commit")
            to = int(req.get("timeout", 30000))
            try:
                page.goto(url, wait_until=wu, timeout=to)
            except Exception as e:
                return {"ok": False, "error": f"navigate failed({type(e).__name__}): {str(e)[:300]}"}
            page.wait_for_timeout(int(req.get("wait_ms", 400)))
            out = {"url": page.url, "title": page.title()[:200]}
            try:
                out["body"] = page.inner_text("body")[:4000]
            except Exception:
                out["body"] = ""
            return {"ok": True, "result": out}
        if act == "get_text":
            sel = req.get("selector")
            if sel:
                els = page.query_selector_all(sel)
                return {"ok": True, "result": [e.inner_text()[:3000] for e in els[:20]]}
            return {"ok": True, "result": page.inner_text("body")[:8000]}
        if act == "click":
            sel = req.get("selector")
            if sel:
                page.click(sel, timeout=int(req.get("timeout", 8000)))
            elif req.get("text"):
                page.get_by_text(req["text"], exact=False).first.click(timeout=8000)
            else:
                return {"ok": False, "error": "click needs selector or text"}
            page.wait_for_timeout(int(req.get("wait_ms", 800)))
            return {"ok": True, "result": {"clicked": sel or req.get("text"), "url": page.url, "title": page.title()}}
        if act == "type":
            sel = req.get("selector")
            if not sel: return {"ok": False, "error": "type needs selector"}
            page.click(sel, timeout=8000)
            page.fill(sel, str(req.get("value", "")))
            if req.get("enter"):
                page.keyboard.press("Enter")
                page.wait_for_timeout(int(req.get("wait_ms", 1500)))
            return {"ok": True, "result": {"typed": True, "url": page.url}}
        if act == "screenshot":
            path = req.get("path") or os.path.join(os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), ".forge-temp"), f"shot_{int(time.time())}.png")
            page.screenshot(path=path, full_page=bool(req.get("full", False)))
            return {"ok": True, "result": {"saved": path, "size": os.path.getsize(path)}}
        if act == "extract":
            sel = req.get("selector")
            attr = req.get("attr")
            els = page.query_selector_all(sel) if sel else [page.locator("body")]
            out = []
            for e in els[:30]:
                out.append(e.get_attribute(attr) if attr else e.inner_text()[:int(req.get('max_chars', 2000))])
            return {"ok": True, "result": out}
        if act == "wait":
            page.wait_for_timeout(int(req.get("ms", 1000)))
            return {"ok": True, "result": "waited"}
        if act == "status":
            return {"ok": True, "result": {"opened": "page" in _ctx, "url": _ctx["page"].url if "page" in _ctx else None}}
        return {"ok": False, "error": f"unknown action: {act}"}
    except Exception as e:
        return {"ok": False, "error": f"{type(e).__name__}: {str(e)[:500]}"}

def main():
    if len(sys.argv) > 1:
        req = json.loads(sys.argv[1])
    else:
        req = json.load(sys.stdin)
    _ctx["headless"] = bool(req.get("headless", False))
    if req.get("channel"): _ctx["channel"] = req["channel"]
    if req.get("proxy"): _ctx["proxy"] = req["proxy"]
    if "steps" in req:  # batch: sequential steps on the same page, so the LLM can decide multiple steps at once
        outs = []
        for st in req["steps"]:
            outs.append(handle(st))
            if not outs[-1].get("ok"):
                break
        out = {"ok": True, "result": outs}
        _close()
    else:
        out = handle(req)
    print(json.dumps(out, ensure_ascii=False))

if __name__ == "__main__":
    main()
