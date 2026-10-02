package main

// wiring_refs_test.go — 接线哨兵的符号引用模型: 接线种类 (wireKind) 与其字符串表示。
// 20260927 自 wiring_sentinel_test.go 拆出 (该文件 F1 shape 8 > 6, 拆分后各自受 F1/F2/F3 约束)。

type wireKind int

const (
	wireCall     wireKind = iota // fn(...)           函数被真正调用
	wireSelector                 // x.Name / pkg.Name  字段或方法被访问
	wireRef                      // 裸标识符           常量/函数值被使用
	// wireMethodCall 方法调用 x.fn(...)。
	// 补这条口径的动机 (20261001): 原 called 只收 `fn(...)` 形态(ast.Ident),
	// 方法调用一律落进 selected —— 而 selected 分不清「真调用」与「仅取方法值」,
	// 且与同名包级函数混淆。新增独立类别而非放宽 wireCall, 避免制造宽口子。
	wireMethodCall
)

func (k wireKind) String() string {
	switch k {
	case wireCall:
		return "调用"
	case wireSelector:
		return "选择器访问"
	case wireMethodCall:
		return "方法调用"
	default:
		return "标识符引用"
	}
}

// symRefs 全包生产 .go 文件的符号引用证据(AST 口径)。
// 类型名不用 prodSymbolRefs: 与扫描函数同名会在同包内 redeclared。
