package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type RegexRequest struct {
	Type     string   `json:"type"`
	Pattern  string   `json:"pattern"`
	Pattern2 string   `json:"pattern2,omitempty"`
	Positive []string `json:"positive,omitempty"`
	Negative []string `json:"negative,omitempty"`
	Flags    string   `json:"flags,omitempty"`
}

type RegexResult struct {
	OK             bool     `json:"ok"`
	Verdict        string   `json:"verdict"`
	Total          int      `json:"total"`
	Passed         int      `json:"passed"`
	Failed         int      `json:"failed"`
	Failures       []string `json:"failures,omitempty"`
	CounterExample string   `json:"counterexample,omitempty"`
	MatchesEmpty   bool     `json:"matches_empty,omitempty"`
	Error          string   `json:"error,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println(`{"ok":false,"error":"usage: regex_gate.exe '<json>'"}`)
		os.Exit(1)
	}

	var req RegexRequest
	if err := json.Unmarshal([]byte(os.Args[1]), &req); err != nil {
		fmt.Printf(`{"ok":false,"error":"%s"}`, err.Error())
		os.Exit(1)
	}

	// Find regex_engine.py next to this executable
	exePath, _ := os.Executable()
	scriptPath := filepath.Join(filepath.Dir(exePath), "regex_engine.py")

	// Pass data via env vars — zero escaping
	os.Setenv("RG_PATTERN", req.Pattern)
	os.Setenv("RG_PATTERN2", req.Pattern2)
	os.Setenv("RG_TYPE", req.Type)
	os.Setenv("RG_FLAGS", req.Flags)
	posJSON, _ := json.Marshal(req.Positive)
	negJSON, _ := json.Marshal(req.Negative)
	os.Setenv("RG_POSITIVE", string(posJSON))
	os.Setenv("RG_NEGATIVE", string(negJSON))

	cmd := exec.Command("python", scriptPath)
	out, err := cmd.Output()
	if err != nil {
		if len(out) > 0 {
			var result RegexResult
			if json.Unmarshal(out, &result) == nil {
				result.OK = true
				json.NewEncoder(os.Stdout).Encode(result)
				return
			}
		}
		fmt.Printf(`{"ok":false,"error":"exec: %s"}`, err.Error())
		os.Exit(1)
	}

	var result RegexResult
	if err := json.Unmarshal(out, &result); err != nil {
		fmt.Printf(`{"ok":false,"error":"parse: %s"}`, err.Error())
		os.Exit(1)
	}
	result.OK = true
	json.NewEncoder(os.Stdout).Encode(result)
}
