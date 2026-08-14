package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type RepairRequest struct {
	Action string `json:"action"`
	Code   string `json:"code"`
	Error  string `json:"error,omitempty"`
	Lang   string `json:"lang,omitempty"`
}

type RepairSuggestion struct {
	Line        int    `json:"line,omitempty"`
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Fix         string `json:"fix,omitempty"`
	Confidence  string `json:"confidence"`
}

type RepairResult struct {
	OK          bool               `json:"ok"`
	Verdict     string             `json:"verdict"`
	Suggestions []RepairSuggestion `json:"suggestions,omitempty"`
	Error       string             `json:"error,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println(`{"ok":false,"error":"usage: repair_gate.exe '<json>'"}`)
		os.Exit(1)
	}

	var req RepairRequest
	if err := json.Unmarshal([]byte(os.Args[1]), &req); err != nil {
		fmt.Printf(`{"ok":false,"error":"%s"}`, err.Error())
		os.Exit(1)
	}

	if req.Action == "" {
		req.Action = "analyze"
	}

	result := analyze(req)
	result.OK = true
	json.NewEncoder(os.Stdout).Encode(result)
}

func analyze(req RepairRequest) RepairResult {
	var suggestions []RepairSuggestion

	// 1. Error message pattern matching
	if req.Error != "" {
		em := strings.ToLower(req.Error)

		// NameError: name 'xxx' is not defined
		re := regexp.MustCompile(`(?i)nameerror.*?name\s+['"](\w+)['"]`)
		if m := re.FindStringSubmatch(req.Error); m != nil {
			suggestions = append(suggestions, RepairSuggestion{
				Severity: "error", Category: "import",
				Description: fmt.Sprintf("Undefined name: '%s'", m[1]),
				Fix:         fmt.Sprintf("Import or define '%s' before use", m[1]),
				Confidence:  "high",
			})
		}

		// ModuleNotFoundError / ImportError
		re2 := regexp.MustCompile(`(?i)(?:no module named|failed to import)\s+['"](\S+?)['"]`)
		if m := re2.FindStringSubmatch(req.Error); m != nil {
			suggestions = append(suggestions, RepairSuggestion{
				Severity: "error", Category: "import",
				Description: fmt.Sprintf("Missing module: '%s'", m[1]),
				Fix:         fmt.Sprintf("Install: pip install %s", m[1]),
				Confidence:  "high",
			})
		}

		// TypeError
		if strings.Contains(em, "typeerror") {
			re3 := regexp.MustCompile(`(?i)typeerror:\s*(.+)`)
			if m := re3.FindStringSubmatch(req.Error); m != nil {
				suggestions = append(suggestions, RepairSuggestion{
					Severity: "error", Category: "type",
					Description: fmt.Sprintf("Type error: %s", strings.TrimSpace(m[1])),
					Fix:         "Check argument types and function signature",
					Confidence:  "medium",
				})
			}
		}

		// AttributeError
		if strings.Contains(em, "attributeerror") {
			re4 := regexp.MustCompile(`(?i)attributeerror:\s*(.+)`)
			if m := re4.FindStringSubmatch(req.Error); m != nil {
				suggestions = append(suggestions, RepairSuggestion{
					Severity: "error", Category: "type",
					Description: fmt.Sprintf("Attribute error: %s", strings.TrimSpace(m[1])),
					Fix:         "Check if the object has this attribute/method",
					Confidence:  "medium",
				})
			}
		}

		// IndentationError / unexpected indent
		if strings.Contains(em, "indentation") || strings.Contains(em, "unexpected indent") {
			suggestions = append(suggestions, RepairSuggestion{
				Severity: "error", Category: "syntax",
				Description: "Indentation error",
				Fix:         "Use consistent 4-space indentation, no tabs",
				Confidence:  "high",
			})
		}

		// KeyError
		re5 := regexp.MustCompile(`(?i)keyerror:\s*(.+)`)
		if m := re5.FindStringSubmatch(req.Error); m != nil {
			suggestions = append(suggestions, RepairSuggestion{
				Severity: "error", Category: "logic",
				Description: fmt.Sprintf("KeyError: %s", strings.TrimSpace(m[1])),
				Fix:         "Use .get(key, default) or check 'if key in dict'",
				Confidence:  "high",
			})
		}

		// SyntaxError
		if strings.Contains(em, "syntaxerror") {
			re6 := regexp.MustCompile(`(?i)syntaxerror:\s*(.+?)(?:\s*\(.*?line\s*(\d+)\))?$`)
			if m := re6.FindStringSubmatch(req.Error); m != nil {
				line := ""
				if len(m) > 2 && m[2] != "" {
					line = fmt.Sprintf(" (line %s)", m[2])
				}
				suggestions = append(suggestions, RepairSuggestion{
					Severity: "error", Category: "syntax",
					Description: fmt.Sprintf("Syntax error: %s%s", strings.TrimSpace(m[1]), line),
					Fix:         "Check for missing colons, brackets, or quotes",
					Confidence:  "high",
				})
			}
		}

		// FileNotFoundError
		if strings.Contains(em, "filenotfounderror") || strings.Contains(em, "no such file") {
			re7 := regexp.MustCompile(`(?i)(?:filenotfounderror|no such file).*?['"]([^'"]+)['"]`)
			if m := re7.FindStringSubmatch(req.Error); m != nil {
				suggestions = append(suggestions, RepairSuggestion{
					Severity: "error", Category: "logic",
					Description: fmt.Sprintf("File not found: '%s'", m[1]),
					Fix:         "Check the file path exists and is spelled correctly",
					Confidence:  "high",
				})
			}
		}
		// ValueError
		if strings.Contains(em, "valueerror") {
			reV := regexp.MustCompile(`(?i)valueerror:\s*(.+)`)
			if m := reV.FindStringSubmatch(req.Error); m != nil {
				suggestions = append(suggestions, RepairSuggestion{
					Severity: "error", Category: "value",
					Description: fmt.Sprintf("Value error: %s", strings.TrimSpace(m[1])),
					Fix:         "Check input values/types before conversion or operation",
					Confidence:  "medium",
				})
			}
		}

		// ZeroDivisionError
		if strings.Contains(em, "zerodivisionerror") {
			reZ := regexp.MustCompile(`(?i)zerodivisionerror:\s*(.+)`)
			if m := reZ.FindStringSubmatch(req.Error); m != nil {
				suggestions = append(suggestions, RepairSuggestion{
					Severity: "error", Category: "value",
					Description: fmt.Sprintf("Division by zero: %s", strings.TrimSpace(m[1])),
					Fix:         "Guard against zero: check divisor before dividing or add epsilon",
					Confidence:  "high",
				})
			}
		}

		// OSError / PermissionError / IsADirectoryError
		if strings.Contains(em, "oserror") || strings.Contains(em, "permissionerror") || strings.Contains(em, "isadirectoryerror") {
			reO := regexp.MustCompile(`(?i)(?:oserror|permissionerror|isadirectoryerror)\s*:\s*(.+)`)
			if m := reO.FindStringSubmatch(req.Error); m != nil {
				suggestions = append(suggestions, RepairSuggestion{
					Severity: "error", Category: "io",
					Description: fmt.Sprintf("OS error: %s", strings.TrimSpace(m[1])),
					Fix:         "Check file permissions, path type (file vs dir), and that resources are not locked",
					Confidence:  "medium",
				})
			}
		}
	}

		// Go: undefined variable/name
		re8 := regexp.MustCompile(`(?i)undefined:\s*(\S+)`)
		if m := re8.FindStringSubmatch(req.Error); m != nil {
			suggestions = append(suggestions, RepairSuggestion{
				Severity: "error", Category: "import",
				Description: fmt.Sprintf("Undeclared name: '%s'", m[1]),
				Fix:         fmt.Sprintf("Declare '%s' with var or := before use", m[1]),
				Confidence:  "high",
			})
		}
		// Go: declared and not used
		re9 := regexp.MustCompile(`(?i)(\S+)\s+declared\s+and\s+not\s+used`)
		if m := re9.FindStringSubmatch(req.Error); m != nil {
			suggestions = append(suggestions, RepairSuggestion{
				Severity: "warning", Category: "style",
				Description: fmt.Sprintf("Unused variable: '%s'", m[1]),
				Fix:         fmt.Sprintf("Remove or use '%s', or assign to _", m[1]),
				Confidence:  "high",
			})
		}
		// Go: cannot use xxx as type
		re10 := regexp.MustCompile(`(?i)cannot\s+use\s+(.+?)\s+as\s+(.+?)\s+(?:in|type)`)
		if m := re10.FindStringSubmatch(req.Error); m != nil {
			suggestions = append(suggestions, RepairSuggestion{
				Severity: "error", Category: "type",
				Description: fmt.Sprintf("Type mismatch: cannot use %s as %s", strings.TrimSpace(m[1]), strings.TrimSpace(m[2])),
				Fix:         "Convert type explicitly or fix assignment",
				Confidence:  "high",
			})
		}

	// 2. Code static analysis (language-agnostic heuristics)
	if req.Code != "" {
		lines := strings.Split(req.Code, "\n")

		// // Bare except detection
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "except:" || trimmed == "except :" {
				suggestions = append(suggestions, RepairSuggestion{
					Line: i + 1, Severity: "warning", Category: "style",
					Description: "Bare except catches all exceptions",
					Fix:         "Use 'except Exception:'",
					Confidence:  "high",
				})
			}
		}

		// Mutable default argument detection
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "def ") && (strings.Contains(trimmed, "=[]") || strings.Contains(trimmed, "={}") || strings.Contains(trimmed, "=set()")) {
				suggestions = append(suggestions, RepairSuggestion{
					Line: i + 1, Severity: "warning", Category: "logic",
					Description: "Mutable default argument",
					Fix:         "Use None as default and init inside function",
					Confidence:  "high",
				})
			}
		}

		// Missing colon after def/if/for/while/else/except/class
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			keywords := []string{"def ", "if ", "elif ", "else", "for ", "while ", "class ", "except", "try", "finally"}
			for _, kw := range keywords {
				if strings.HasPrefix(trimmed, kw) && !strings.HasSuffix(strings.TrimSpace(trimmed), ":") {
					// 'else' and 'except' and 'try' and 'finally' MUST end with :
					if kw == "else" || kw == "except" || kw == "try" || kw == "finally" {
						suggestions = append(suggestions, RepairSuggestion{
							Line: i + 1, Severity: "error", Category: "syntax",
							Description: fmt.Sprintf("Missing colon after '%s'", kw),
							Fix:         fmt.Sprintf("Add ':' at end: '%s:'", trimmed),
							Confidence:  "high",
						})
					}
				}
			}
		}

		// Unbalanced parentheses per line
		for i, line := range lines {
			open := strings.Count(line, "(") - strings.Count(line, ")")
			if open > 0 && open <= 3 {
				suggestions = append(suggestions, RepairSuggestion{
					Line: i + 1, Severity: "warning", Category: "syntax",
					Description: fmt.Sprintf("Possibly missing %d closing parenthesis", open),
					Fix:         fmt.Sprintf("Add %d ')' at end of line", open),
					Confidence:  "low",
				})
			}
		}

		// Mixed tabs and spaces
		hasTab := false
		hasSpace := false
		for _, line := range lines {
			if strings.HasPrefix(line, "	") {
				hasTab = true
			}
			if strings.HasPrefix(line, "    ") {
				hasSpace = true
			}
		}
		if hasTab && hasSpace {
			suggestions = append(suggestions, RepairSuggestion{
				Severity: "warning", Category: "style",
				Description: "Mixed tabs and spaces for indentation",
				Fix:         "Use 4 spaces consistently (convert tabs to spaces)",
				Confidence:  "high",
			})
		}

		// Trailing whitespace
		trailing := 0
		for _, line := range lines {
			if len(line) > 0 && (strings.HasSuffix(line, " ") || strings.HasSuffix(line, "	")) {
				trailing++
			}
		}
		if trailing > 0 {
			suggestions = append(suggestions, RepairSuggestion{
				Severity: "info", Category: "style",
				Description: fmt.Sprintf("%d line(s) with trailing whitespace", trailing),
				Fix:         "Remove trailing spaces",
				Confidence:  "high",
			})
		}
	}


	// Go-specific: undeclared variable detection (= vs :=)
	if strings.Contains(req.Code, "func ") && (strings.Contains(req.Code, "package ") || strings.Contains(req.Code, "import ")) {
		codeLines := strings.Split(req.Code, "\n")
		declared := make(map[string]bool)
		insideFunc := false
		for i, cl := range codeLines {
			trimmed := strings.TrimSpace(cl)
			if strings.Contains(trimmed, "func ") {
				insideFunc = true
				declared = make(map[string]bool)
				reParams := regexp.MustCompile(`func\s+\w*\s*\(([^)]*)\)`)
				if pm := reParams.FindStringSubmatch(trimmed); pm != nil {
					for _, p := range strings.Split(pm[1], ",") {
						p = strings.TrimSpace(p)
						if idx := strings.Index(p, " "); idx > 0 {
							p = p[:idx]
						}
						if p != "" {
							declared[p] = true
						}
					}
				}
			}
			reWalrus := regexp.MustCompile(`(\w+)\s*:=`)
			if wm := reWalrus.FindAllStringSubmatch(trimmed, -1); wm != nil {
				for _, w := range wm {
					declared[w[1]] = true
				}
			}
			reVar := regexp.MustCompile(`var\s+(\w+)`)
			if vm := reVar.FindStringSubmatch(trimmed); vm != nil {
				declared[vm[1]] = true
			}
			if insideFunc && !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") {
				reAssign := regexp.MustCompile(`^(\w+)\s*=\s*[^=]`)
				if am := reAssign.FindStringSubmatch(trimmed); am != nil {
					vname := am[1]
					if vname != "err" && vname != "nil" && vname != "true" && vname != "false" && vname != "iota" && vname != "_" {
						if !declared[vname] {
							suggestions = append(suggestions, RepairSuggestion{
								Line: i + 1, Severity: "error", Category: "syntax",
								Description: fmt.Sprintf("Undeclared variable: '%s' (use := for declaration)", vname),
								Fix:         fmt.Sprintf("Change '%s =' to '%s :=' or add 'var %s' before use", vname, vname, vname),
								Confidence:  "high",
							})
						}
					}
				}
			}
		}
	}
	// Dedup
	seen := make(map[string]bool)
	var unique []RepairSuggestion
	for _, s := range suggestions {
		key := fmt.Sprintf("%d|%s|%s", s.Line, s.Category, s.Description[:min(50, len(s.Description))])
		if !seen[key] {
			seen[key] = true
			unique = append(unique, s)
		}
	}

	verdict := "no_issues"
	if len(unique) > 0 {
		hasError := false
		for _, s := range unique {
			if s.Severity == "error" {
				hasError = true
				break
			}
		}
		if hasError {
			verdict = "needs_manual"
		} else {
			verdict = "suggestions"
		}
	}

	return RepairResult{
		Verdict:     verdict,
		Suggestions: unique,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
