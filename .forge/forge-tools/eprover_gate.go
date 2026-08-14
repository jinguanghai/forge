package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)


// TPTP FOF parser + DPLL SAT solver
// Handles: ~ & | => <=> ![X] ?[X] (quantifiers stripped for prop)
// Propositional only; quantifiers return "eprover_required"

type Clause []int // list of literals: positive=var, negative=-var

type EproverResult struct {
	OK      bool   `json:"ok"`
	Verdict string `json:"verdict"`   // theorem, counter_sat, eprover_required
	Output  string `json:"output"`
	Solver  string `json:"solver"`
	Error   string `json:"error,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println(`{"ok":false,"verdict":"error","error":"usage: eprover_gate <tptp_code>  or  eprover_gate --probe"}`)
		os.Exit(1)
	}

	input := os.Args[1]
	if input == "--probe" {
		fmt.Println(`{"ok":true,"verdict":"available","output":"Built-in DPLL solver for propositional FOF. FOL (quantified) problems are rejected -- use lang=logic (Z3) for those.","solver":"dpll"}`)
		return
	}

	result := solve(input)

	json.NewEncoder(os.Stdout).Encode(result)
}

func solve(tptpInput string) EproverResult {
	// Parse FOF formulas
	axioms, conjecture, err := parseTPTP(tptpInput)
	if err != "" {
		return EproverResult{OK: true, Verdict: "error", Solver: "dpll", Error: err}
	}

	if conjecture == nil {
		return EproverResult{OK: true, Verdict: "error", Solver: "dpll", Error: "no conjecture found"}
	}

	// Check for quantifiers
	for _, f := range append(axioms, conjecture) {
		if f.hasQuantifier {
			return EproverResult{
				OK:      true,
				Verdict: "eprover_required",
				Output:  "FOL quantifiers detected. This gate is propositional-only (DPLL). Use lang=logic (Z3) for first-order problems, or provide a propositional FOF formula.",
				Solver:  "none",
			}
		}
	}

	// Convert to CNF and run DPLL
	// Theorem: axioms & ~conjecture is UNSAT
	var allClauses []Clause
	for _, f := range axioms {
		allClauses = append(allClauses, toCNF(f)...)
	}
	negConj := negateFormula(conjecture)
	allClauses = append(allClauses, toCNF(negConj)...)

	// Simplify clauses (remove tautologies)
	allClauses = simplify(allClauses)

sat := dpll(allClauses)


	if sat {
		return EproverResult{
			OK:      true,
			Verdict: "counter_sat",
			Output:  "Solved via built-in DPLL (FOF->CNF). Verdict: counter_sat",
			Solver:  "dpll",
		}
	}
	return EproverResult{
		OK:      true,
		Verdict: "theorem",
		Output:  "Solved via built-in DPLL (FOF->CNF). Verdict: theorem",
		Solver:  "dpll",
	}
}

// ============================================================
// Formula AST
// ============================================================

type formulaKind int

const (
	fkAtom   formulaKind = iota
	fkNot
	fkAnd
	fkOr
	fkImplies
	fkEquiv
)

type formula struct {
	kind          formulaKind
	name          string // for atoms
	left, right   *formula
	hasQuantifier bool
}

func atomFormula(name string) *formula {
	return &formula{kind: fkAtom, name: name}
}

func notFormula(child *formula) *formula {
	return &formula{kind: fkNot, left: child}
}

func andFormula(l, r *formula) *formula {
	return &formula{kind: fkAnd, left: l, right: r}
}

func orFormula(l, r *formula) *formula {
	return &formula{kind: fkOr, left: l, right: r}
}

func impliesFormula(l, r *formula) *formula {
	return &formula{kind: fkImplies, left: l, right: r}
}

func equivFormula(l, r *formula) *formula {
	return &formula{kind: fkEquiv, left: l, right: r}
}

// ============================================================
// TPTP Parser (FOF subset)
// ============================================================

func parseTPTP(input string) (axioms []*formula, conjecture *formula, err string) {
	// Split into formulas
	raw := input
	// Remove comments
	lines := strings.Split(raw, "\n")
	var clean []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") {
			continue
		}
		clean = append(clean, line)
	}
	raw = strings.Join(clean, "\n")

	// Parse each fof() formula
	for {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			break
		}
		if !strings.HasPrefix(raw, "fof(") {
			return nil, nil, "expected fof(, got: " + truncate(raw, 30)
		}
		// Find matching closing ")." for this fof
		end := findFofEnd(raw)
		if end < 0 {
			return nil, nil, "unclosed fof()"
		}
		fofStr := raw[:end+1] // include the final .
		raw = raw[end+1:]

		f, role, parseErr := parseFof(fofStr)
		if parseErr != "" {
			return nil, nil, parseErr
		}
		switch role {
		case "axiom", "hypothesis", "lemma", "assumption":
			axioms = append(axioms, f)
		case "conjecture":
			if conjecture != nil {
				return nil, nil, "multiple conjectures"
			}
			conjecture = f
		default:
			// treat other roles as axioms
			axioms = append(axioms, f)
		}
	}

	return
}

func findFofEnd(s string) int {
	// fof(name, role, formula).
	// Find the ".\n" or ".$" after matching parens
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '.':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func parseFof(s string) (*formula, string, string) {
	// fof(name, role, formula).
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "fof(") {
		return nil, "", "not an fof"
	}
	s = s[4:] // skip "fof("
	// name
	comma1 := strings.Index(s, ",")
	if comma1 < 0 {
		return nil, "", "no comma after name"
	}
	// name := strings.TrimSpace(s[:comma1])
	s = s[comma1+1:]
	// role
	comma2 := strings.Index(s, ",")
	if comma2 < 0 {
		return nil, "", "no comma after role"
	}
	role := strings.TrimSpace(s[:comma2])
	s = s[comma2+1:]
	// formula - everything until ")."
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, ").") {
		return nil, "", "fof must end with )."
	}
	formulaStr := s[:len(s)-2] // remove ")."
	f, _, err := parseFormula(strings.TrimSpace(formulaStr))
	return f, role, err
}

func parseFormula(s string) (*formula, string, string) {
	return parseEquiv(s)
}

// Precedence: Equiv < Implies < Or < And < Unary/Atom
// Each level parses its operator as right-associative

func parseEquiv(s string) (*formula, string, string) {
	left, rest, err := parseImplies(s)
	if err != "" {
		return nil, rest, err
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "<=>") {
		right, rest2, err2 := parseEquiv(strings.TrimSpace(rest[3:]))
		if err2 != "" {
			return nil, rest, err2
		}
		return equivFormula(left, right), rest2, ""
	}
	return left, rest, ""
}

func parseImplies(s string) (*formula, string, string) {
	left, rest, err := parseOr(s)
	if err != "" {
		return nil, rest, err
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "=>") {
		right, rest2, err2 := parseImplies(strings.TrimSpace(rest[2:]))
		if err2 != "" {
			return nil, rest, err2
		}
		return impliesFormula(left, right), rest2, ""
	}
	return left, rest, ""
}

func parseOr(s string) (*formula, string, string) {
	left, rest, err := parseAnd(s)
	if err != "" {
		return nil, rest, err
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "|") {
		right, rest2, err2 := parseOr(strings.TrimSpace(rest[1:]))
		if err2 != "" {
			return nil, rest, err2
		}
		return orFormula(left, right), rest2, ""
	}
	return left, rest, ""
}

func parseAnd(s string) (*formula, string, string) {
	left, rest, err := parseUnary(s)
	if err != "" {
		return nil, rest, err
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "&") {
		right, rest2, err2 := parseAnd(strings.TrimSpace(rest[1:]))
		if err2 != "" {
			return nil, rest, err2
		}
		return andFormula(left, right), rest2, ""
	}
	return left, rest, ""
}

func parseUnary(s string) (*formula, string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, s, "empty formula"
	}

	// Quantifiers
	if strings.HasPrefix(s, "!") || strings.HasPrefix(s, "?") {
		qend := strings.Index(s, ":")
		if qend < 0 {
			return nil, s, "quantifier without :"
		}
		body := strings.TrimSpace(s[qend+1:])
		f, rest, err := parseFormula(body)
		if f != nil {
			f.hasQuantifier = true
		}
		return f, rest, err
	}

	// Parenthesized
	if s[0] == '(' {
		end := matchParen(s)
		if end < 0 {
			return nil, s, "unmatched ("
		}
		inner := s[1:end]
		f, _, err := parseFormula(inner)
		return f, strings.TrimSpace(s[end+1:]), err
	}

	// ~ (unary negation)
	if s[0] == '~' {
		child, rest, err := parseUnary(strings.TrimSpace(s[1:]))
		if err != "" {
			return nil, s, err
		}
		return notFormula(child), rest, ""
	}

	// Atom
	i := 0
	for i < len(s) && (isAlphaNum(s[i]) || s[i] == '_') {
		i++
	}
	if i == 0 {
		return nil, s, "expected atom at: " + truncate(s, 20)
	}
	return atomFormula(s[:i]), s[i:], ""
}

func matchParen(s string) int {
	if s[0] != '(' {
		return -1
	}
	depth := 1
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func isAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// ============================================================
// CNF Conversion (Tseitin-like but simple for propositional)
// ============================================================

func negateFormula(f *formula) *formula {
	return notFormula(f)
}

func toCNF(f *formula) []Clause {
	// Eliminate <=> and =>
	f = eliminateConnectors(f)
	// Push negations inward (De Morgan)
	f = pushNegations(f)
	// Distribute | over &
	f = distribute(f)
	// Collect clauses
	return collectClauses(f)
}

func eliminateConnectors(f *formula) *formula {
	if f == nil {
		return nil
	}
	switch f.kind {
	case fkAtom:
		return f
	case fkNot:
		return notFormula(eliminateConnectors(f.left))
	case fkAnd:
		return andFormula(eliminateConnectors(f.left), eliminateConnectors(f.right))
	case fkOr:
		return orFormula(eliminateConnectors(f.left), eliminateConnectors(f.right))
	case fkImplies:
		// a => b  =  ~a | b
		return orFormula(notFormula(eliminateConnectors(f.left)), eliminateConnectors(f.right))
	case fkEquiv:
		// a <=> b  =  (~a | b) & (a | ~b)
		l := eliminateConnectors(f.left)
		r := eliminateConnectors(f.right)
		return andFormula(
			orFormula(notFormula(l), r),
			orFormula(l, notFormula(r)),
		)
	}
	return f
}

func pushNegations(f *formula) *formula {
	if f == nil {
		return nil
	}
	switch f.kind {
	case fkAtom:
		return f
	case fkNot:
		child := f.left
		switch child.kind {
		case fkAtom:
			return f // ~atom, keep
		case fkNot:
			// ~~X -> X
			return pushNegations(child.left)
		case fkAnd:
			// ~(A & B) -> ~A | ~B
			return orFormula(pushNegations(notFormula(child.left)), pushNegations(notFormula(child.right)))
		case fkOr:
			// ~(A | B) -> ~A & ~B
			return andFormula(pushNegations(notFormula(child.left)), pushNegations(notFormula(child.right)))
		default:
			return f
		}
	case fkAnd:
		return andFormula(pushNegations(f.left), pushNegations(f.right))
	case fkOr:
		return orFormula(pushNegations(f.left), pushNegations(f.right))
	default:
		return f
	}
}

func distribute(f *formula) *formula {
	if f == nil {
		return nil
	}
	f.left = distribute(f.left)
	f.right = distribute(f.right)

	if f.kind == fkOr {
		// (A & B) | C  ->  (A | C) & (B | C)
		if f.left != nil && f.left.kind == fkAnd {
			return andFormula(
				distribute(orFormula(f.left.left, f.right)),
				distribute(orFormula(f.left.right, f.right)),
			)
		}
		// A | (B & C)  ->  (A | B) & (A | C)
		if f.right != nil && f.right.kind == fkAnd {
			return andFormula(
				distribute(orFormula(f.left, f.right.left)),
				distribute(orFormula(f.left, f.right.right)),
			)
		}
	}
	return f
}

func collectClauses(f *formula) []Clause {
	if f == nil {
		return nil
	}
	switch f.kind {
	case fkAnd:
		left := collectClauses(f.left)
		right := collectClauses(f.right)
		return append(left, right...)
	case fkOr:
		lits := collectLits(f)
		return []Clause{lits}
	case fkAtom:
		return []Clause{{varToInt(f.name)}}
	case fkNot:
		if f.left.kind == fkAtom {
			return []Clause{{-varToInt(f.left.name)}}
		}
	}
	return nil
}

func collectLits(f *formula) []int {
	if f == nil {
		return nil
	}
	switch f.kind {
	case fkOr:
		return append(collectLits(f.left), collectLits(f.right)...)
	case fkAtom:
		return []int{varToInt(f.name)}
	case fkNot:
		if f.left.kind == fkAtom {
			return []int{-varToInt(f.left.name)}
		}
	}
	return nil
}

// ============================================================
// Variable mapping
// ============================================================

var varMap = make(map[string]int)
var nextVar = 0

func varToInt(name string) int {
	if v, ok := varMap[name]; ok {
		return v
	}
	nextVar++
	varMap[name] = nextVar
	return nextVar
}

// ============================================================
// DPLL SAT Solver
// ============================================================

func simplify(clauses []Clause) []Clause {
	var out []Clause
	for _, c := range clauses {
		// Remove duplicate literals
		seen := make(map[int]bool)
		var dedup Clause
		tautology := false
		for _, lit := range c {
			if seen[-lit] {
				tautology = true
				break
			}
			if !seen[lit] {
				seen[lit] = true
				dedup = append(dedup, lit)
			}
		}
		if !tautology && len(dedup) > 0 {
			out = append(out, dedup)
		}
	}
	return out
}

func dpll(clauses []Clause) bool {
	// Reset var map for each solve
	varMap = make(map[string]int)
	nextVar = 0

	return dpllRec(clauses, make(map[int]bool))
}

func dpllRec(clauses []Clause, assignment map[int]bool) bool {
	// Unit propagation
	clauses, changed := unitPropagate(clauses, assignment)
	if clauses == nil {
		return false
	}
	if len(clauses) == 0 {
		return true
	}
	if !changed {
		// Pure literal elimination
		clauses = pureLiteralElim(clauses, assignment)
		if len(clauses) == 0 {
			return true
		}
		// Pick a literal
		lit := clauses[0][0]
		v := lit
		if v < 0 {
			v = -v
		}
		if _, used := assignment[v]; !used {
			// Try true
			assignTrue := make(map[int]bool)
			for k, v := range assignment {
				assignTrue[k] = v
			}
			assignTrue[v] = true
			if dpllRec(clauses, assignTrue) {
				return true
			}
			// Try false
			assignFalse := make(map[int]bool)
			for k, v := range assignment {
				assignFalse[k] = v
			}
			assignFalse[v] = false
			if dpllRec(clauses, assignFalse) {
				return true
			}
		}
		return false
	}
	return dpllRec(clauses, assignment)
}

func unitPropagate(clauses []Clause, assignment map[int]bool) ([]Clause, bool) {
	changed := false
	for {
		var unitLit int
		foundUnit := false
		for _, c := range clauses {
			unassigned := 0
			last := 0
			satisfied := false
			for _, lit := range c {
				v := lit
				if v < 0 {
					v = -v
				}
				if val, ok := assignment[v]; ok {
					if (lit > 0 && val) || (lit < 0 && !val) {
						satisfied = true
						break
					}
				} else {
					unassigned++
					last = lit
				}
			}
			if satisfied {
				continue
			}
			if unassigned == 0 {
				return nil, false // conflict
			}
			if unassigned == 1 {
				unitLit = last
				foundUnit = true
				break
			}
		}
		if !foundUnit {
			break
		}
		// Assign unit literal
		v := unitLit
		if v < 0 {
			v = -v
		}
		assignment[v] = (unitLit > 0)
		changed = true

		// Remove satisfied clauses
		var newClauses []Clause
		for _, c := range clauses {
			satisfied := false
			keep := true
			var reduced Clause
			for _, lit := range c {
				vl := lit
				if vl < 0 {
					vl = -vl
				}
				if val, ok := assignment[vl]; ok {
					if (lit > 0 && val) || (lit < 0 && !val) {
						satisfied = true
						break
					}
					// literal falsified, remove from clause
					continue
				}
				reduced = append(reduced, lit)
			}
			if satisfied {
				continue
			}
			if len(reduced) == 0 {
				return nil, false // conflict
			}
			if keep {
				newClauses = append(newClauses, reduced)
			}
		}
		clauses = newClauses
		if clauses == nil {
			clauses = []Clause{}
		}
	}
	return clauses, changed
}

func pureLiteralElim(clauses []Clause, assignment map[int]bool) []Clause {
	// Count literal polarities
	pos := make(map[int]bool)
	neg := make(map[int]bool)
	for _, c := range clauses {
		for _, lit := range c {
			v := lit
			if v < 0 {
				v = -v
				neg[v] = true
			} else {
				pos[v] = true
			}
		}
	}
	// Pure literals: appears only positive or only negative
	pure := make(map[int]bool)
	for v := range pos {
		if !neg[v] {
			pure[v] = true
			assignment[v] = true
		}
	}
	for v := range neg {
		if !pos[v] {
			pure[v] = true
			assignment[v] = false
		}
	}
	if len(pure) == 0 {
		return clauses
	}
	// Remove clauses containing pure literals
	var out []Clause
	for _, c := range clauses {
		keep := true
		for _, lit := range c {
			v := lit
			if v < 0 {
				v = -v
			}
			if pure[v] {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, c)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
