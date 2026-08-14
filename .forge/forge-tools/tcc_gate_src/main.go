package main

import (
    "embed"
    "fmt"
    "os"
    "os/exec"
    "path"
    "path/filepath"
)

//go:embed tcc_embed/*
var tccFiles embed.FS

func main() {
    if len(os.Args) < 2 {
        fmt.Fprintln(os.Stderr, "tcc_gate: no C code provided")
        os.Exit(1)
    }
    code := os.Args[1]

    tmpDir, err := os.MkdirTemp("", "tcc_gate_")
    if err != nil {
        fmt.Fprintf(os.Stderr, "tcc_gate: mkdir temp: %v\n", err)
        os.Exit(1)
    }
    defer os.RemoveAll(tmpDir)

    if err := extractEmbedded(tmpDir); err != nil {
        fmt.Fprintf(os.Stderr, "tcc_gate: extract: %v\n", err)
        os.Exit(1)
    }

    srcFile := filepath.Join(tmpDir, "code.c")
    if err := os.WriteFile(srcFile, []byte(code), 0644); err != nil {
        fmt.Fprintf(os.Stderr, "tcc_gate: write source: %v\n", err)
        os.Exit(1)
    }

    tccExe := filepath.Join(tmpDir, "tcc.exe")
    cmd := exec.Command(tccExe, "-run", srcFile)
    cmd.Dir = tmpDir

    output, err := cmd.CombinedOutput()
    os.Stdout.Write(output)

    if err != nil {
        if exitErr, ok := err.(*exec.ExitError); ok {
            os.Exit(exitErr.ExitCode())
        }
        os.Exit(1)
    }
}

func extractEmbedded(dst string) error {
    return extractDir("tcc_embed", dst)
}

func extractDir(srcDir, dstDir string) error {
    if err := os.MkdirAll(dstDir, 0755); err != nil {
        return err
    }
    entries, err := tccFiles.ReadDir(srcDir)
    if err != nil {
        return fmt.Errorf("readdir %s: %w", srcDir, err)
    }
    for _, entry := range entries {
        srcPath := path.Join(srcDir, entry.Name())
        dstPath := filepath.Join(dstDir, entry.Name())
        if entry.IsDir() {
            if err := extractDir(srcPath, dstPath); err != nil {
                return err
            }
        } else {
            data, err := tccFiles.ReadFile(srcPath)
            if err != nil {
                return fmt.Errorf("read %s: %w", srcPath, err)
            }
            if err := os.WriteFile(dstPath, data, 0644); err != nil {
                return fmt.Errorf("write %s: %w", dstPath, err)
            }
        }
    }
    return nil
}
