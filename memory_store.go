package main

// memory_store.go — memory.json 双版本保护(借鉴 MemHop A/B 双头思想)
//
// 核心: 任何时刻工作目录中至少有一份完整 JSON。
//   - memory.json      = 最新成功版本
//   - memory.json.bak  = 上一成功版本(降级保底)
// 写入顺序: ① 旧主文件(有效时)升为 .bak → ② 新数据原子写主文件。
// 任何一步崩溃, 目录中都保留着可用的旧版本。

import (
	"encoding/json"
	"fmt"
	"os"
)

// backupMemoryPath 记忆备份路径(上一成功版本)
func backupMemoryPath(workDir string) string {
	return memoryFilePath(workDir) + ".bak"
}

// atomicWrite 原子替换: 写 tmp 再 rename(避免写半截覆盖有效文件)
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadMemory 读取记忆; 主文件缺失/损坏时自动回退 .bak。
// 返回 (数据, 是否从备份恢复, 错误)。恢复成功但返回错误的情况不会发生。
func LoadMemory(workDir string) ([]byte, bool, error) {
	path := memoryFilePath(workDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 主文件缺失 → 尝试 .bak
			bak, berr := os.ReadFile(backupMemoryPath(workDir))
			if berr != nil {
				return nil, false, fmt.Errorf("memory.json 缺失且 .bak 不可读: %v", berr)
			}
			if json.Valid(bak) {
				return bak, true, nil
			}
			return nil, false, fmt.Errorf("memory.json 缺失且 .bak 无效")
		}
		return nil, false, err
	}
	if json.Valid(data) {
		return data, false, nil
	}
	// 主文件损坏 → 回退 .bak
	bak, berr := os.ReadFile(backupMemoryPath(workDir))
	if berr == nil && json.Valid(bak) {
		return bak, true, nil
	}
	return nil, false, fmt.Errorf("memory.json 损坏且 .bak 无效")
}

// SaveMemory 原子保存记忆, 并保留上一成功版本为 .bak。
// 旧主文件若已损坏则跳过备份(不覆盖现有有效 .bak)。
func SaveMemory(workDir string, data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("SaveMemory: 数据不是合法 JSON")
	}
	// 写入前体检 (UTF-8 + 必填锚点字段)
	if err := memHealthLint(data); err != nil {
		return fmt.Errorf("SaveMemory: %v", err)
	}
	// 频率护栏 (同日第 2 次写锚点 → 错误, 要求合并改动)
	if err := anchorGuardCheck(workDir); err != nil {
		return err
	}
	path := memoryFilePath(workDir)
	// ① 旧主文件(有效时)升为 .bak; 首次保存时 .bak 初始化本次数据
	oldData, readErr := os.ReadFile(path)
	if readErr == nil && json.Valid(oldData) {
		if err := atomicWrite(backupMemoryPath(workDir), oldData); err != nil {
			return fmt.Errorf("SaveMemory: 备份旧版失败: %v", err)
		}
	} else if os.IsNotExist(readErr) {
		if err := atomicWrite(backupMemoryPath(workDir), data); err != nil {
			return fmt.Errorf("SaveMemory: 初始化备份失败: %v", err)
		}
	}
	// ② 新数据原子写主文件
	err := atomicWrite(path, data)
	if err == nil {
		logEvent(EvMemoryUpdate, "SaveMemory", nil)
		// 审计留痕 (改了什么字段)
		anchorGuardAudit(workDir, anchorChangedFields(oldData, data), "")
	}
	return err
}

// HealMemory 启动自愈: 主文件损坏 → 用 .bak 覆盖恢复。返回是否发生了恢复。
func HealMemory(workDir string) (bool, error) {
	path := memoryFilePath(workDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // 无主文件属正常(首次运行)
		}
		return false, err
	}
	if json.Valid(data) {
		return false, nil
	}
	bak, berr := os.ReadFile(backupMemoryPath(workDir))
	if berr != nil || !json.Valid(bak) {
		return false, fmt.Errorf("memory.json 损坏且 .bak 不可用, 无法自愈")
	}
	if err := atomicWrite(path, bak); err != nil {
		return false, err
	}
	return true, nil
}
