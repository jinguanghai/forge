package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type LogicRequest struct {
	Type string `json:"type"`
	Code string `json:"code"`
}

type LogicResult struct {
	OK      bool   `json:"ok"`
	Verdict string `json:"verdict,omitempty"`
	Model   string `json:"model,omitempty"`
	Error   string `json:"error,omitempty"`
}

// extractIdentifiers returns plausible free variable names from an expression.
// Used by the SAT auto-wrap so "x > 0" becomes x = Int('x'); s.add(x > 0).
var identRE = regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*)\b`)

var pyKeywords = map[string]bool{
	"and": true, "or": true, "not": true, "if": true, "else": true,
	"for": true, "while": true, "def": true, "return": true, "import": true,
	"from": true, "in": true, "is": true, "None": true, "True": true,
	"False": true, "lambda": true, "assert": true, "print": true,
	"s": true, "Solver": true, "Int": true, "Real": true, "Bool": true,
	"Ints": true, "Reals": true, "Bools": true, "And": true, "Or": true,
	"Not": true, "Implies": true, "sat": true, "unsat": true, "unknown": true,
}

// stripComments removes # comment lines and inline " #" comments, then
// collapses remaining lines into a single space-joined expression.
func stripComments(code string) string {
	var out []string
	for _, line := range strings.Split(code, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, " ")
}

func extractFreeVars(expr string) []string {
	seen := map[string]bool{}
	var vars []string
	for _, m := range identRE.FindAllString(expr, -1) {
		if pyKeywords[m] || seen[m] {
			continue
		}
		// Skip things that look like method calls / attributes (.name) and digits
		if strings.HasSuffix(m, "()") {
			continue
		}
		if m[0] >= '0' && m[0] <= '9' {
			continue
		}
		seen[m] = true
		vars = append(vars, m)
	}
	return vars
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println(`{"ok":false,"error":"usage: logic_gate.exe '<json>'"}`)
		os.Exit(1)
	}

	var req LogicRequest
	if err := json.Unmarshal([]byte(os.Args[1]), &req); err != nil {
		fmt.Printf(`{"ok":false,"error":"%s"}`, err.Error())
		os.Exit(1)
	}

	code := req.Code
	var py strings.Builder
	py.WriteString("import json, sys\n")
	py.WriteString("from z3 import *\n\n")

	switch req.Type {
	case "prove":
		// User must define claim (and typically s with constraints).
		py.WriteString(code + "\n\n")
		py.WriteString("s.add(Not(claim))\n")
		py.WriteString("r = s.check()\n")
		py.WriteString("if r == unsat:\n    out = {'verdict': 'proved'}\n")
		py.WriteString("elif r == sat:\n    out = {'verdict': 'disproved', 'model': str(s.model())}\n")
		py.WriteString("else:\n    out = {'verdict': 'unknown'}\n")

	case "equivalence":
		// User must define a and b.
		py.WriteString(code + "\n\n")
		py.WriteString("s = Solver()\n")
		py.WriteString("s.add(Not(a == b))\n")
		py.WriteString("r = s.check()\n")
		py.WriteString("if r == unsat:\n    out = {'verdict': 'equivalent'}\n")
		py.WriteString("elif r == sat:\n    out = {'verdict': 'not_equivalent', 'model': str(s.model())}\n")
		py.WriteString("else:\n    out = {'verdict': 'unknown'}\n")

	default: // "sat" — auto-wrap expressions when no explicit solver code is given.
		if strings.Contains(code, "Solver()") || strings.Contains(code, "s.add") {
			// Full user code: they manage the solver themselves.
			py.WriteString(code + "\n\n")
			py.WriteString("out = {}\n")
			py.WriteString("r = s.check()\n")
			py.WriteString("out['verdict'] = str(r)\n")
			py.WriteString("if r == sat:\n    out['model'] = str(s.model())\n")
		} else if strings.Contains(code, "claim") {
			// User defines claim but no solver: build it for them.
			py.WriteString(code + "\n\n")
			py.WriteString("s = Solver()\n")
			py.WriteString("s.add(claim)\n")
			py.WriteString("r = s.check()\n")
			py.WriteString("out = {'verdict': str(r)}\n")
			py.WriteString("if r == sat:\n    out['model'] = str(s.model())\n")
		} else {
			// Bare expression: strip comments, collapse lines, infer free vars as Ints.
			cleanExpr := stripComments(code)
			if strings.TrimSpace(cleanExpr) == "" {
				fmt.Printf(`{"ok":false,"error":"empty expression after comment strip; provide a logic expression (e.g. x > 0 & x < 10) or JSON {type,code}"}`)
				os.Exit(1)
			}
			vars := extractFreeVars(cleanExpr)
			// Heuristic: if the expression mixes arithmetic comparisons, treat
			// free vars as Int; if it is pure boolean (& | ~), treat them as Bool.
			isArith := strings.ContainsAny(cleanExpr, "+-*/<>=")
			for _, v := range vars {
				if isArith {
					py.WriteString(v + " = Int('" + v + "')\n")
				} else {
					py.WriteString(v + " = Bool('" + v + "')\n")
				}
			}
			py.WriteString("s = Solver()\n")
			py.WriteString("s.add(" + cleanExpr + ")\n")
			py.WriteString("r = s.check()\n")
			py.WriteString("out = {'verdict': str(r)}\n")
			py.WriteString("if r == sat:\n    out['model'] = str(s.model())\n")
		}
	}

	py.WriteString("\nprint(json.dumps(out))\n")

	tmpFile, tmpErr := os.CreateTemp("", "logic_gate_*.py")
	if tmpErr != nil {
		fmt.Printf(`{"ok":false,"error":"tmp: %s"}`, tmpErr.Error())
		os.Exit(1)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	tmpFile.WriteString(py.String())
	tmpFile.Close()

	cmd := exec.Command("python", tmpPath)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	outBytes, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if stderrBuf.Len() > 0 {
			msg = strings.TrimSpace(stderrBuf.String())
			if len(msg) > 500 {
				msg = msg[:500] + "..."
			}
		}
		fmt.Printf(`{"ok":false,"error":"Z3 error: %s"}`, strings.ReplaceAll(msg, `"`, `'`))
		os.Exit(1)
	}

	var result LogicResult
	if err := json.Unmarshal(outBytes, &result); err != nil {
		fmt.Printf(`{"ok":false,"error":"parse: %s","raw":"%s"}`, err.Error(), string(outBytes))
		os.Exit(1)
	}
	result.OK = true
	json.NewEncoder(os.Stdout).Encode(result)
}
