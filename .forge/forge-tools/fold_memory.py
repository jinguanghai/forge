# Fold-Unfold Memory System —— fold_memory.py (deterministic folding script)
# Usage: python fold_memory.py '<JSON>'
# JSON: {"name":"task name","summary":"one-line summary","detail":"details (multi-line)","status":"done|doing|shelved"}
# Effect: 1) write archive _archive\folded_memory\<id>.md (standard template)
#         2) append an index entry to memory.json folded_memory.items (schema v2, replace by name)
import json, os, sys, datetime

FORGE = os.environ.get("FORGE_WORK_DIR") or os.getcwd()
if not os.path.exists(os.path.join(FORGE, "memory.json")):
    FORGE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MEM = os.path.join(FORGE, "memory.json")
FOLD_DIR = os.path.join(FORGE, "_archive", "folded_memory")


def main():
    if len(sys.argv) < 2:
        print(json.dumps({"OK": False, "error": "missing parameter: JSON"}, ensure_ascii=False))
        return
    try:
        p = json.loads(sys.argv[1])
    except Exception as e:
        print(json.dumps({"OK": False, "error": f"JSON parse failed: {e}"}, ensure_ascii=False))
        return
    name = (p.get("name") or "").strip()
    summary = (p.get("summary") or "").strip()
    detail = (p.get("detail") or "").strip()
    status = (p.get("status") or "done").strip()
    if not name or not summary:
        print(json.dumps({"OK": False, "error": "name/summary are required"}, ensure_ascii=False))
        return

    os.makedirs(FOLD_DIR, exist_ok=True)
    today = datetime.date.today().strftime("%Y%m%d")
    n = 1
    while True:
        fid = f"fold_{today}_{n:02d}"
        fp = os.path.join(FOLD_DIR, fid + ".md")
        if not os.path.exists(fp):
            break
        n += 1

    doc = f"""# {name}
- Status: {status}
- Folded at: {today}
- Summary: {summary}
- Details:
{detail}
"""
    with open(fp, "w", encoding="utf-8") as f:
        f.write(doc)

    mem = json.load(open(MEM, encoding="utf-8"))
    fm = mem.setdefault("folded_memory", {})
    fm.setdefault("_说明", "Folded items: completed / low-attention items. The normal view only shows one-line indexes (no attention cost); when the owner issues 展开<name>, Forge reads the corresponding archive to restore full details.")
    items = fm.setdefault("items", [])
    for i, it in enumerate(items):
        if it.get("name") == name:
            items.pop(i)
            break
    items.append({
        "id": fid,
        "name": name,
        "status": status,
        "summary": summary,
        "archive": os.path.join("_archive", "folded_memory", fid + ".md").replace("\\", "\\\\"),
        "folded_at": today,
        "last_unfolded": "",
        "tier": 1,
    })
    with open(MEM, "w", encoding="utf-8") as f:
        json.dump(mem, f, ensure_ascii=False, indent=2)
    print(json.dumps({"OK": True, "id": fid, "archive": fp, "folded": name}, ensure_ascii=False))


if __name__ == "__main__":
    main()
