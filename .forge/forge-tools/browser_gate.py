# -*- coding: utf-8 -*-
"""browser_gate: 铸剑炉浏览器gate (Playwright原子操作)
输入: {"action":"navigate|get_text|click|type|screenshot|extract|wait|close|status", ...}
输出: {"ok":true,"result":...} 或 {"ok":false,"error":"..."}
安全: 只读为主; 点击/输入需显式指令; 禁止下载执行文件; 默认有头可配headless
"""
import sys, json, io, os, time
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

try:
    from playwright.sync_api import sync_playwright
except Exception as e:
    print(json.dumps({"ok": False, "error": f"playwright未安装: {e}"}, ensure_ascii=False)); sys.exit(0)

_ctx = {"channel": "msedge"}

_CN_DOMAINS = {".cn", "baidu.com", "qq.com", "163.com", "taobao.com", "tmall.com", "jd.com",
              "zhihu.com", "weibo.com", "douyin.com", "bilibili.com", "sina.com.cn", "sohu.com",
              "360.cn", "xinhuanet.com", "people.com.cn", "csdn.net", "gitee.com", "aliyun.com",
              "tencent.com", "bytedance.com", "meituan.com", "dianping.com", "xueqiu.com", "bing.com", "microsoft.com"}

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
    """Edge 不支持 socks5h(ERR_NO_SUPPORTED_PROXIES); 本地bridge(1081)把 HTTP CONNECT 转发为 socks5h(VPS端解析DNS);
    1081在线→走http bridge(Edge兼容); 否则回退socks5(Playwright原生支持, 去掉h前缀)"""
    if p and p.startswith(("socks5://", "socks5h://")):
        if _http_bridge_alive():
            return "http://127.0.0.1:1081"
        return "socks5://" + p.split("://", 1)[1]  # playwright不支持socks5h前缀
    return p

def _ensure(proxy=None):
    if "page" in _ctx and _ctx["page"] is not None and _ctx.get("proxy") == proxy:
        return _ctx["page"]
    _close()
    pw = sync_playwright().start()
    headless = _ctx.get("headless", False)
    kw = {}
    if proxy:
        kw["proxy"] = {"server": _map_proxy(proxy)}  # socks5h原样: 代理端解析DNS防污染
    else:
        # 直连: --no-proxy-server阻止chromium读系统代理; 但不清环境变量(handle里_env_proxy()还要读取)
        kw["args"] = ["--no-proxy-server"]
    browser = pw.chromium.launch(headless=headless, channel=_ctx.get("channel", "msedge"), **kw)
    page = browser.new_page()
    page.set_default_timeout(20000)
    _ctx.update(pw=pw, browser=browser, page=page, proxy=proxy)
    return page

def _close():
    # page/browser 用 .close(); pw(Playwright实例) 必须用 .stop() —— .close()不存在,
    # 若被except吞掉会导致pw泄漏, 第二次sync_playwright().start()报asyncio错误
    for k in ("page", "browser"):
        v = _ctx.pop(k, None)
        try: v.close() if v else None
        except Exception: pass
    v = _ctx.pop("pw", None)
    if v is not None:
        try: v.stop()
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
                return {"ok": False, "error": f"navigate失败({type(e).__name__}): {str(e)[:300]}"}
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
                return {"ok": False, "error": "click需要selector或text"}
            page.wait_for_timeout(int(req.get("wait_ms", 800)))
            return {"ok": True, "result": {"clicked": sel or req.get("text"), "url": page.url, "title": page.title()}}
        if act == "type":
            sel = req.get("selector")
            if not sel: return {"ok": False, "error": "type需要selector"}
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
        return {"ok": False, "error": f"未知action: {act}"}
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
    if "steps" in req:  # 批处理: 同一页面顺序执行, 供LLM一次决策多步
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
