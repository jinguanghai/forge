package main

// sh_retired.go — sh gate 退役回告文本 (20261001, 六西格玛 DMAIC 改善项)。
//
// 为何独立成文件: forge.go 是核心编排文件, 其顶层声明数受分形守卫 F1 约束
// (shape ≤ 6)。把退役文本内联在 forge.go 会让 shape 6→7 触发"上帝文件"退化告警。
// 这也符合单一职责: 退役策略(文本+哨兵契约)与 Build 编排分离。
//
// 哨兵: sh_retired_test.go 断言本文本含 "subprocess"(拒绝必须自带出路)。

// shGateRetiredText 是 sh gate 退役后的统一回告。
// 拒绝必须自带出路: 实测 sh 失败后模型重试率 47.3%(瞎试), 而给出 python 示例一次即可改对。
// 文本含 "subprocess" 是哨兵 sh_retired_test.go 的断言目标。
const shGateRetiredText = `sh gate 已于 20261001 退役。
实测 226 次调用失败率 48.2% (全炉最高, 唯一 fallback 源); 根因是 Windows 上它实际执行
cmd.exe, Unix 语法命令 52.3% 直接报 "is not recognized"。
shell 能力请改用 python 的 subprocess 承接 —— 能力等价 (18/18 能力矩阵实测通过):

  import subprocess
  r = subprocess.run(["git", "status"], capture_output=True, text=True,
                     encoding="utf-8", errors="replace")
  print(r.returncode)
  print(r.stdout)

要点:
  · 命令与参数写成列表(不要拼 shell 字符串), 避免转义与注入问题
  · 需要超时: subprocess.run(..., timeout=10) —— 超时抛 TimeoutExpired
  · 中文/非 ASCII 输出: 必加 encoding="utf-8", errors="replace"
  · 管道/通配符: 用 python 自身(列表推导/glob), 不要依赖 shell
  · Windows 原生命令: subprocess.run(["cmd", "/c", "dir"], ...)`
