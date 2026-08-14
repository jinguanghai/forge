import json, re, os, sys, itertools

pattern = os.environ.get("RG_PATTERN", "")
pattern2 = os.environ.get("RG_PATTERN2", "")
typ = os.environ.get("RG_TYPE", "test")
flags_str = os.environ.get("RG_FLAGS", "")
positive = json.loads(os.environ.get("RG_POSITIVE", "[]")) or []
negative = json.loads(os.environ.get("RG_NEGATIVE", "[]")) or []

flags = 0
for c in flags_str:
    if c == "i": flags |= re.IGNORECASE
    if c == "m": flags |= re.MULTILINE
    if c == "s": flags |= re.DOTALL

def test_match():
    total = len(positive) + len(negative)
    passed = 0
    failures = []
    for s in positive:
        if re.fullmatch(pattern, s, flags):
            passed += 1
        else:
            failures.append("POS:" + repr(s))
    for s in negative:
        if not re.fullmatch(pattern, s, flags):
            passed += 1
        else:
            failures.append("NEG:" + repr(s))
    me = bool(re.fullmatch(pattern, "", flags))
    return {"verdict": "all_pass" if not failures else "has_failures",
            "total": total, "passed": passed, "failed": len(failures),
            "failures": failures, "matches_empty": me}

def check_equivalent():
    try:
        from greenery import parse
        eq = parse(pattern).equivalent(parse(pattern2))
        return {"verdict": "equivalent" if eq else "not_equivalent",
                "total": 0, "passed": 0, "failed": 0}
    except:
        pass
    chars = sorted(set(re.findall(r'[a-zA-Z0-9]', pattern + pattern2)))[:10]
    if not chars:
        chars = ['a', 'b', '0', '1']
    for L in range(6):
        for combo in itertools.product(chars, repeat=L):
            s = ''.join(combo)
            if bool(re.fullmatch(pattern, s, flags)) != bool(re.search(pattern2, flags)):
                return {"verdict": "not_equivalent", "counterexample": s,
                        "total": 0, "passed": 0, "failed": 0}
    return {"verdict": "likely_equivalent", "total": 0, "passed": 0, "failed": 0}

def find_ce():
    chars = sorted(set(re.findall(r'[a-zA-Z0-9]', pattern + pattern2)))[:10]
    if not chars:
        chars = ['a', 'b', '0', '1']
    for L in range(8):
        for combo in itertools.product(chars, repeat=L):
            s = ''.join(combo)
            m1 = bool(re.fullmatch(pattern, s, flags))
            m2 = bool(re.search(pattern2, s, flags))
            if m1 != m2:
                return {"verdict": "found", "counterexample": s,
                        "total": 0, "passed": 0, "failed": 0}
    return {"verdict": "no_counterexample_found", "total": 0, "passed": 0, "failed": 0}

try:
    if typ == "equivalent":
        result = check_equivalent()
    elif typ == "find_counterexample":
        result = find_ce()
    else:
        result = test_match()
    result["ok"] = True
    print(json.dumps(result))
except Exception as e:
    print(json.dumps({"ok": False, "verdict": "error", "error": str(e)}))
