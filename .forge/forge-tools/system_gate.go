package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// SystemRequest: a model-checking problem specification.
// The user provides Python code that defines:
//   init() -> state       (initial state as dict)
//   next_states(state) -> [state] (list of successor states)
//   invariant(state) -> bool (property that must hold in all reachable states)
//   target(state) -> bool  (ONLY for type=reachability: goal predicate)
//
// State-transition semantics (QM-style state transition table):
//   - Every reachable state is visited via BFS from init().
//   - A state with NO successors is a DEADLOCK (terminal state). Deadlocks are
//     reported in deadlock_states (max 10). For "check", a deadlock is NOT an
//     invariant violation by itself; for "reachability" a terminal state that
//     satisfies target() is the goal.
//   - Causal consistency: successors must be produced from a valid state; the
//     engine rejects next_states() errors with a trace to the offending state.
type SystemRequest struct {
	Type      string `json:"type"`      // "check" (default) or "reachability"
	Code      string `json:"code"`      // Python: defines init(), next_states(), invariant() and optionally target()
	MaxStates int    `json:"max_states,omitempty"` // default 100000
}

type SystemResult struct {
	OK             bool     `json:"ok"`
	Verdict        string   `json:"verdict"`         // check: passed/violated/exhausted/error; reachability: reachable/unreachable/exhausted/error
	StatesExplored int      `json:"states_explored"`
	InvariantHolds bool     `json:"invariant_holds"`
	CounterExample []string `json:"counterexample,omitempty"` // trace to invariant violation
	DeadlockStates []string `json:"deadlock_states,omitempty"` // terminal states (no successors), max 10
	Path           []string `json:"path,omitempty"`  // reachability mode: witness path to target
	Error          string   `json:"error,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println(`{"ok":false,"error":"usage: system_gate.exe '<json>' "}`)
		os.Exit(1)
	}

	var req SystemRequest
	if err := json.Unmarshal([]byte(os.Args[1]), &req); err != nil {
		fmt.Printf(`{"ok":false,"error":"%s"}`, err.Error())
		os.Exit(1)
	}

	if req.MaxStates == 0 {
		req.MaxStates = 100000
	}
	if req.Type == "" {
		req.Type = "check"
	}

	py := buildModelCheckerPy(req)

	tmpFile, tmpErr := os.CreateTemp("", "system_gate_*.py")
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
			var result SystemResult
			if json.Unmarshal(out, &result) == nil && result.Verdict != "" {
				result.OK = true
				json.NewEncoder(os.Stdout).Encode(result)
				return
			}
		}
		fmt.Printf(`{"ok":false,"error":"execution failed: %s","stderr":"%s"}`,
			err.Error(), string(out))
		os.Exit(1)
	}

	var result SystemResult
	if err := json.Unmarshal(out, &result); err != nil {
		fmt.Printf(`{"ok":false,"error":"parse: %s","raw":"%s"}`,
			err.Error(), strings.TrimSpace(string(out)))
		os.Exit(1)
	}
	result.OK = true
	json.NewEncoder(os.Stdout).Encode(result)
}

func buildModelCheckerPy(req SystemRequest) string {
	if req.Type == "reachability" {
		return fmt.Sprintf(`import json

# User-defined model (reachability mode: init, next_states, target required)
%s

def run_reach(max_states):
    init_state = init()
    def canon(s):
        return json.dumps(s, sort_keys=True, default=str)
    visited = {}
    queue = [(init_state, [canon(init_state)])]
    visited[canon(init_state)] = True
    states_explored = 0
    while queue and states_explored < max_states:
        state, path = queue.pop(0)
        states_explored += 1
        try:
            if target(state):
                return {"verdict": "reachable", "states_explored": states_explored, "invariant_holds": True, "path": path}
        except Exception as e:
            return {"verdict": "error", "states_explored": states_explored, "error": "target check failed: " + str(e) + " at state " + canon(state)}
        try:
            successors = next_states(state)
        except Exception as e:
            return {"verdict": "error", "states_explored": states_explored, "error": "next() failed: " + str(e) + " at state " + canon(state)}
        for succ in successors:
            sk = canon(succ)
            if sk not in visited:
                visited[sk] = True
                queue.append((succ, path + [canon(succ)]))
    if states_explored >= max_states:
        return {"verdict": "exhausted", "states_explored": states_explored, "path": []}
    return {"verdict": "unreachable", "states_explored": states_explored, "path": []}

result = run_reach(%d)
print(json.dumps(result))
`, req.Code, req.MaxStates)
	}

	// default: "check" mode
	return fmt.Sprintf(`import json

# User-defined model (check mode: init, next_states, invariant required)
%s

def run_check(max_states):
    init_state = init()
    def canon(s):
        return json.dumps(s, sort_keys=True, default=str)
    visited = {}
    queue = [(init_state, [])]
    visited[canon(init_state)] = True
    states_explored = 0
    deadlocks = []
    while queue and states_explored < max_states:
        state, trace = queue.pop(0)
        states_explored += 1
        try:
            if not invariant(state):
                return {"verdict": "violated", "states_explored": states_explored, "invariant_holds": False, "counterexample": trace + [canon(state)]}
        except Exception as e:
            return {"verdict": "error", "states_explored": states_explored, "error": "invariant check failed: " + str(e) + " at state " + canon(state)}
        try:
            successors = next_states(state)
        except Exception as e:
            return {"verdict": "error", "states_explored": states_explored, "error": "next() failed: " + str(e) + " at state " + canon(state)}
        if not successors:
            if len(deadlocks) < 10:
                deadlocks.append(canon(state))
            continue
        for succ in successors:
            sk = canon(succ)
            if sk not in visited:
                visited[sk] = True
                queue.append((succ, trace + [canon(state)]))
    if states_explored >= max_states:
        return {"verdict": "exhausted", "states_explored": states_explored, "invariant_holds": True, "deadlock_states": deadlocks}
    return {"verdict": "passed", "states_explored": states_explored, "invariant_holds": True, "deadlock_states": deadlocks}

result = run_check(%d)
print(json.dumps(result))
`, req.Code, req.MaxStates)
}
