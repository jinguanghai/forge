# 折叠展开记忆系统 —— fold_memory.py（确定性折叠脚本）
# 用法: python fold_memory.py '<JSON>'
# JSON: {"name":"任务名","summary":"一句话摘要","detail":"细节(可多行)","status":"done|doing|shelved"}
# 作用: ① 写档案 _archive\folded_memory\<id>.md（标准模板）
#       ② memory.json folded_memory.items 追加一行索引（schema v2, 同名替换）
import json, os, sys, datetime

FORGE = os.environ.get("FORGE_WORK_DIR") or os.getcwd()
if not os.path.exists(os.path.join(FORGE, "memory.json")):
    FORGE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MEM = os.path.join(FORGE, "memory.json")
FOLD_DIR = os.path.join(FORGE, "_archive", "folded_memory")


def main():
    if len(sys.argv) < 2:
        print(json.dumps({"OK": False, "error": "缺少参数: JSON"}, ensure_ascii=False))
        return
    try:
        p = json.loads(sys.argv[1])
    except Exception as e:
        print(json.dumps({"OK": False, "error": f"JSON解析失败: {e}"}, ensure_ascii=False))
        return
    name = (p.get("name") or "").strip()
    summary = (p.get("summary") or "").strip()
    detail = (p.get("detail") or "").strip()
    status = (p.get("status") or "done").strip()
    if not name or not summary:
        print(json.dumps({"OK": False, "error": "name/summary 必填"}, ensure_ascii=False))
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
- 状态: {status}
- 折叠日期: {today}
- 摘要: {summary}
- 细节:
{detail}
"""
    with open(fp, "w", encoding="utf-8") as f:
        f.write(doc)

    mem = json.load(open(MEM, encoding="utf-8"))
    fm = mem.setdefault("folded_memory", {})
    fm.setdefault("_说明", "折叠区：已完成/暂不关注的事项。正常视角只看到items里的一行行索引（不占注意力），主人指令'展开<名称>'时铸剑炉去读对应档案恢复全部细节。")
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
