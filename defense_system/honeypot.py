# -*- coding: utf-8 -*-
"""蜜罐: 模拟SSH/HTTP服务, 诱捕并记录攻击者行为. 只监听本机, 记录不反击."""
import socket, threading, datetime, json, os, sys
sys.stdout.reconfigure(encoding='utf-8', errors='replace')

LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "logs", "honeypot.jsonl")
PORT_SSH, PORT_HTTP = 2222, 8080

def _record(entry):
    os.makedirs(os.path.dirname(LOG), exist_ok=True)
    entry["ts"] = datetime.datetime.now().isoformat(timespec="seconds")
    with open(LOG, "a", encoding="utf-8") as f:
        f.write(json.dumps(entry, ensure_ascii=False) + "\n")

def ssh_handler(conn, addr):
    ip, port = addr
    conn.sendall(b"SSH-2.0-OpenSSH_7.4 fake banner\r\n")
    buf = b""
    try:
        while True:
            data = conn.recv(4096)
            if not data: break
            buf += data
            if b"\n" in buf:
                line = buf.strip().decode("utf-8", "replace")
                _record({"type": "honeypot_ssh", "src_ip": ip, "src_port": port,
                         "data": line[:500], "service": "ssh"})
                buf = b""
    except Exception as e:
        _record({"type": "honeypot_ssh_err", "src_ip": ip, "src_port": port, "err": str(e)})
    finally:
        conn.close()

def http_handler(conn, addr):
    ip, port = addr
    try:
        data = conn.recv(8192).decode("utf-8", "replace")
        head = data.split("\r\n\r\n")[0]
        first = head.split("\r\n")[0] if head else ""
        ua = ""
        for ln in head.split("\r\n"):
            if ln.lower().startswith("user-agent:"):
                ua = ln.split(":", 1)[1].strip()
        _record({"type": "honeypot_http", "src_ip": ip, "src_port": port,
                 "request": first, "ua": ua[:200], "service": "http"})
        resp = b"HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\n\r\n"
        conn.sendall(resp)
    except Exception:
        pass
    finally:
        conn.close()

def _serve(port, handler, name):
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port))
    s.listen(50)
    print(f"[honeypot] {name} 监听 0.0.0.0:{port}")
    while True:
        c, a = s.accept()
        threading.Thread(target=handler, args=(c, a), daemon=True).start()

def main():
    threading.Thread(target=_serve, args=(PORT_SSH, ssh_handler, "SSH"), daemon=True).start()
    threading.Thread(target=_serve, args=(PORT_HTTP, http_handler, "HTTP"), daemon=True).start()
    print("[honeypot] 运行中, Ctrl+C 退出. 日志:", LOG)
    try:
        threading.Event().wait()
    except KeyboardInterrupt:
        print("[honeypot] 已停止")

if __name__ == "__main__":
    main()
