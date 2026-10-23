package main

// exe 名的单一来源 (T22 第 2 层)。
//
// 旧版各处写死 "forge.exe" / "forge_new.exe" —— 在 Linux 上自升级会找错文件名
// (编译产物是 forge 而非 forge.exe), 表现为「编译成功但新版本未生成」。
// 抽成函数后 Windows 行为逐字节不变(exeSuffix() 返回 ".exe"), 非 Windows 得无后缀名。

// exeBaseName 是铸剑炉主程序基名(不含平台后缀)。
const exeBaseName = "forge"

// mainExeName 返回生产二进制文件名 (Windows: forge.exe / Linux 等: forge)。
func mainExeName() string { return exeBaseName + exeSuffix() }

// newExeName 返回自升级编译产物名 (Windows: forge_new.exe / Linux 等: forge_new)。
func newExeName() string { return exeBaseName + "_new" + exeSuffix() }
