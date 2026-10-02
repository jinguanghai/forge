package main

// forge_exec_util.go — 通用进程/归档工具 (原 forge_shell.go)。
//
// 历史: 本文件原为 sh gate 的 Windows 执行器选择层 (POSIX 特征判定 → Git Bash / cmd.exe,
// 含 17 条 "is not recognized" 缺陷的修复)。sh gate 已于 20261001 退役 —— 实测 47 天
// 31239 次调用中失败率 48.2% (全炉最高, 唯一 fallback 源), 根因是 Windows 上实际执行
// cmd.exe 而输入是 Unix 语法。相关判定函数 (cmdShellBuiltins / posixShellTools /
// bashOnlyKeywords / bashOnlySubstrings / cmdOnlyWords / cmdOnlySubstrings / cmdOnlyMarkers /
// containsCmdMarker / shCmdOnlyVerdict / forgeFindBash / shPrefersBash / splitShellSegments /
// shEchoStaticPattern / forgeSplitCommand) 一并删除, 判据见 sh_retired_test.go。
//
// 现存 expandArgs / safeExitCode / forgeUnzip / forgeUntarGz —— 被 forge_gate_file.go 与
// forge_env.go 使用, 与 sh 无关, 故保留。

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// expandArgs 在参数数组中展开 {file}/{forge}/{dir} 占位符，返回展开后的参数和是否含 {file} 占位符。
func expandArgs(args []string, srcPath, workDir, tmpDir string) ([]string, bool) {
	expanded := make([]string, len(args))
	copy(expanded, args)
	place := false
	for i, a := range expanded {
		if a == "{file}" {
			expanded[i] = srcPath
			place = true
		}
		if strings.Contains(a, "{forge}") {
			expanded[i] = strings.ReplaceAll(a, "{forge}", workDir)
			if runtime.GOOS != "windows" {
				expanded[i] = strings.ReplaceAll(expanded[i], ".exe", "")
			}
		}
		if strings.Contains(a, "{dir}") {
			expanded[i] = strings.ReplaceAll(a, "{dir}", tmpDir)
		}
	}
	return expanded, place
}

// safeExitCode returns the exit code of a command, or -1 if ProcessState is nil.
func safeExitCode(cmd *exec.Cmd) int {
	if cmd.ProcessState == nil {
		return -1
	}
	return cmd.ProcessState.ExitCode()
}

// forgeUnzip 解压 zip 文件到目标目录
func forgeUnzip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		targetPath := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("zip slip detected: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(targetPath, 0755)
			continue
		}
		os.MkdirAll(filepath.Dir(targetPath), 0755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// forgeUntarGz 解压 tar.gz 文件到目标目录
func forgeUntarGz(tarGzPath, destDir string) error {
	fh, err := os.Open(tarGzPath)
	if err != nil {
		return err
	}
	defer fh.Close()
	gzReader, err := gzip.NewReader(fh)
	if err != nil {
		return err
	}
	defer gzReader.Close()
	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		targetPath := filepath.Join(destDir, header.Name)
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("tar slip detected: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(targetPath, 0755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(targetPath), 0755)
			outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		}
	}
	return nil
}
