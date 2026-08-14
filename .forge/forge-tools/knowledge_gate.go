package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type KnowledgeRequest struct {
	Type     string `json:"type"`
	Query    string `json:"query"`
	Endpoint string `json:"endpoint,omitempty"`
	Timeout  int    `json:"timeout,omitempty"`
}

type KnowledgeResult struct {
	OK      bool             `json:"ok"`
	Source  string           `json:"source"`
	Verdict string           `json:"verdict"`
	Results []map[string]any `json:"results,omitempty"`
	Count   int              `json:"count"`
	Error   string           `json:"error,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println(`{"ok":false,"error":"usage: knowledge_gate.exe '<json>'"}`)
		os.Exit(1)
	}

	var req KnowledgeRequest
	if err := json.Unmarshal([]byte(os.Args[1]), &req); err != nil {
		fmt.Printf(`{"ok":false,"error":"%s"}`, err.Error())
		os.Exit(1)
	}

	if req.Endpoint == "" {
		req.Endpoint = "auto"
	}
	if req.Timeout == 0 {
		req.Timeout = 30
	}

	py := buildKnowledgePy(req)

	// Write to temp file instead of -c (Windows compat)
	tmpFile, tmpErr := os.CreateTemp("", "knowledge_gate_*.py")
	if tmpErr != nil {
		fmt.Printf(`{"ok":false,"error":"tmp: %s"}`, tmpErr.Error())
		os.Exit(1)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	tmpFile.WriteString(py)
	tmpFile.Close()

	cmd := exec.Command("python", tmpPath)
	out, err := cmd.Output()
	if err != nil {
		if len(out) > 0 {
			var result KnowledgeResult
			if json.Unmarshal(out, &result) == nil {
				result.OK = true
				json.NewEncoder(os.Stdout).Encode(result)
				return
			}
		}
		fmt.Printf(`{"ok":false,"error":"execution: %s","stderr":"%s"}`,
			err.Error(), string(out))
		os.Exit(1)
	}

	var result KnowledgeResult
	if err := json.Unmarshal(out, &result); err != nil {
		fmt.Printf(`{"ok":false,"error":"parse: %s","raw":"%s"}`,
			err.Error(), strings.TrimSpace(string(out)))
		os.Exit(1)
	}
	result.OK = true
	json.NewEncoder(os.Stdout).Encode(result)
}

func buildKnowledgePy(req KnowledgeRequest) string {
	q := strings.ReplaceAll(req.Query, `\`, `\\`)
	q = strings.ReplaceAll(q, `"`, `\"`)
	q = strings.ReplaceAll(q, "\n", " ")
	q = strings.ReplaceAll(q, "\r", "")

	return fmt.Sprintf(`import json, sys, urllib.request, urllib.parse, urllib.error

endpoints = {
    "wikidata": "https://query.wikidata.org/sparql",
    "dbpedia": "https://dbpedia.org/sparql",
}

def query_sparql(endpoint_url, sparql_query, timeout):
    params = urllib.parse.urlencode({"format": "json", "query": sparql_query})
    url = endpoint_url + "?" + params
    req = urllib.request.Request(url, headers={"User-Agent": "CrushKnowledgeGate/1.0"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        bindings = data.get("results", {}).get("bindings", [])
        return {"ok": True, "bindings": bindings, "count": len(bindings)}
    except urllib.error.HTTPError as e:
        code = e.code
        if code == 429:
            return {"ok": False, "error": "rate_limited", "detail": str(e)}
        if code == 400:
            return {"ok": False, "error": "bad_request", "detail": "SPARQL syntax error -- provide a valid SPARQL query (natural language is not accepted). " + str(e)}
        return {"ok": False, "error": "http_error", "detail": "HTTP " + str(code) + ": " + str(e)}
    except Exception as e:
        msg = str(e)
        if "timeout" in msg.lower():
            return {"ok": False, "error": "timeout", "detail": msg}
        return {"ok": False, "error": "query_failed", "detail": msg}

query = "%s"
ep = "%s"
timeout = %d

result = None
source = ""

if ep == "auto":
    for name in ["dbpedia", "wikidata"]:
        r = query_sparql(endpoints[name], query, timeout)
        if r["ok"]:
            result = r
            source = name
            break
        # Any failure (http/timeout/rate_limit): keep trying next endpoint (fallback)
        result = r
        source = name
else:
    r = query_sparql(endpoints.get(ep, endpoints["wikidata"]), query, timeout)
    result = r
    source = ep

if result is None:
    print(json.dumps({"source":"none","verdict":"error","error":"all endpoints failed","count":0}))
    sys.exit(0)

if not result["ok"]:
    print(json.dumps({
        "source": source,
        "verdict": "error",
        "error": result.get("error", "unknown"),
        "count": 0,
    }))
    sys.exit(0)

bindings = result["bindings"]
count = len(bindings)

verdict = "unknown"
if count > 0:
    verdict = "confirmed"
elif count == 0 and result["ok"]:
    verdict = "no_results"

# Simplify bindings with three-tier content classification (QM-style):
#   Tier-1 deterministic identity: URI/bnode identifiers -> kept as-is with _type marker
#   Tier-2 pre-existing facts: literal strings -> kept as-is (no extra keys, backward compatible)
#   Tier-3 runtime metadata: datatype/xml:lang -> attached only when present
#   So a URI variable gets xxx_type="uri", a typed literal gets xxx_datatype, a
#   language-tagged string gets xxx_lang. Plain strings stay clean.
simple = []
for b in bindings[:20]:
    item = {}
    for k, v in b.items():
        val = v.get("value", str(v))
        item[k] = val
        t = v.get("type", "literal")
        if t != "literal" or "datatype" in v or "xml:lang" in v:
            item[k + "_type"] = t
            if "datatype" in v:
                item[k + "_datatype"] = v["datatype"]
            if "xml:lang" in v:
                item[k + "_lang"] = v["xml:lang"]
    simple.append(item)

print(json.dumps({
    "source": source,
    "verdict": verdict,
    "count": count,
    "results": simple,
}, default=str))
`, q, req.Endpoint, req.Timeout)
}
