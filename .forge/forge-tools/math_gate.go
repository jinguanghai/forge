package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// MathGate is a DEAD program. Its behavior is fixed at compile time.
// LLM cannot negotiate with it, cannot persuade it, cannot bypass it.
// It either produces a verified SymPy result, or it produces an error.
// There is no third option.

type MathRequest struct {
	Expr     string `json:"expr"`
	Action   string `json:"action"`   // simplify, solve, equals, evaluate, factor, integrate, diff
	Expected string `json:"expected,omitempty"`
}

type MathResult struct {
	OK     bool   `json:"ok"`
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
	SymPy  string `json:"sympy_raw"`
}

func main() {
	if len(os.Args) < 2 {
		// stdin mode
		fmt.Println(`{"ok":false,"error":"usage: math_gate.exe '<json_request>' or stdin"}`)
		os.Exit(1)
	}

	var req MathRequest
	if err := json.Unmarshal([]byte(os.Args[1]), &req); err != nil {
		fmt.Printf(`{"ok":false,"error":"invalid JSON: %s"}`, err.Error())
		os.Exit(1)
	}

	if req.Expr == "" {
		fmt.Println(`{"ok":false,"error":"expr is required"}`)
		os.Exit(1)
	}

	if req.Action == "" {
		req.Action = "simplify"
	}

	// Build SymPy verification script
	pyCode := buildSymPyScript(req)
	
	// Write to temp file to avoid Windows -c escaping issues
	tmpFile, tmpErr := os.CreateTemp("", "math_gate_*.py")
	if tmpErr != nil {
		fmt.Printf(`{"ok":false,"error":"tmp: %s"}`, tmpErr.Error())
		os.Exit(1)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	tmpFile.WriteString(pyCode)
	tmpFile.Close()
	
	cmd := exec.Command("python", tmpPath)
	out, err := cmd.Output()
	if err != nil {
		fmt.Printf(`{"ok":false,"error":"SymPy execution failed: %s"}`, err.Error())
		os.Exit(1)
	}

	var result MathResult
	if err := json.Unmarshal(out, &result); err != nil {
		fmt.Printf(`{"ok":false,"error":"SymPy output parse failed: %s","sympy_raw":"%s"}`, err.Error(), strings.TrimSpace(string(out)))
		os.Exit(1)
	}

	// SymPy may have reported an error (its script exits 0 even on failure).
	// Propagate that failure instead of forcing OK=true.
	if !result.OK || result.Error != "" {
		if result.Error == "" {
			result.Error = "sympy evaluation failed"
		}
		json.NewEncoder(os.Stdout).Encode(result)
		os.Exit(1)
	}

	result.OK = true
	json.NewEncoder(os.Stdout).Encode(result)
}

// stripPyComments removes # comments and collapses lines so multi-line notes
// (e.g. LLM annotations) never reach parse_expr.
func stripPyComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, " ")
}

func buildSymPyScript(req MathRequest) string {
	// Sanitize: prevent code injection
	expr := stripPyComments(req.Expr)
	expr = strings.ReplaceAll(expr, `"`, `\"`)
	// Replace ^ with ** for exponentiation (sympy interprets ^ as XOR)
	expr = strings.ReplaceAll(expr, "^", "**")
	expected := strings.ReplaceAll(req.Expected, `"`, `\"`)
	expected = strings.ReplaceAll(expected, "^", "**")
	
	return fmt.Sprintf(`import json,sys
from sympy import *
from sympy.parsing.sympy_parser import parse_expr,standard_transformations,implicit_multiplication_application
try:
    tr=standard_transformations+(implicit_multiplication_application,)
    e=parse_expr("%s",transformations=tr)
    action="%s"
    if action=="simplify":
        r=simplify(e)
    elif action=="solve":
        x=symbols('x');r=solve(e,x)
    elif action=="equals":
        ex=parse_expr("%s",transformations=tr);d=simplify(e-ex);r=(d==0)
    elif action=="evaluate":
        r=N(e,50)
    elif action=="factor":
        r=factor(e)
    elif action=="integrate":
        x=symbols('x');r=integrate(e,x)
    elif action=="diff":
        x=symbols('x');r=diff(e,x)
    else:
        r=simplify(e)
    print(json.dumps({"ok":True,"result":str(r),"latex":latex(r) if r is not None else "None"}))
except Exception as ex:
    print(json.dumps({"ok":False,"error":str(ex)}))
`, expr, req.Action, expected)
}
