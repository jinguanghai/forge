
# sympy_verify.py — 死边界数学验证器
# 用法: python sympy_verify.py '<json_input>'
# 输入: {"expr": "表达式", "expected": "期望值(可选)", "action": "simplify|solve|equals|evaluate"}
# 输出: {"pass": true/false, "result": "...", "sympy_output": "..."}

import sys, json
from sympy import *
from sympy.parsing.sympy_parser import parse_expr, standard_transformations, implicit_multiplication_application

def verify(input_data):
    action = input_data.get("action", "evaluate")
    expr_str = input_data.get("expr", "")
    expected_str = input_data.get("expected", None)
    
    transformations = standard_transformations + (implicit_multiplication_application,)
    
    try:
        expr = parse_expr(expr_str, transformations=transformations)
    except Exception as e:
        return {"pass": False, "error": f"Parse error: {e}", "result": None}
    
    try:
        if action == "simplify":
            result = simplify(expr)
        elif action == "solve":
            x = symbols('x')
            result = solve(expr, x)
        elif action == "equals":
            expected = parse_expr(expected_str, transformations=transformations) if expected_str else None
            if expected is not None:
                diff = simplify(expr - expected)
                result = diff == 0
            else:
                return {"pass": False, "error": "equals requires 'expected' field"}
        elif action == "evaluate":
            result = N(expr, 50)
        elif action == "factor":
            result = factor(expr)
        elif action == "integrate":
            x = symbols('x')
            result = integrate(expr, x)
        elif action == "diff":
            x = symbols('x')
            result = diff(expr, x)
        else:
            result = simplify(expr)
        
        return {
            "pass": True,
            "result": str(result),
            "latex": latex(result) if result is not None else None
        }
    except Exception as e:
        return {"pass": False, "error": f"Computation error: {e}", "result": None}

if __name__ == "__main__":
    if len(sys.argv) > 1:
        try:
            data = json.loads(sys.argv[1])
        except:
            data = {"expr": sys.argv[1], "action": "simplify"}
    else:
        # stdin mode
        data = json.loads(sys.stdin.read())
    
    result = verify(data)
    print(json.dumps(result, ensure_ascii=False))
