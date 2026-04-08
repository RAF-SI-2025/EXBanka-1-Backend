package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	fmt.Println("=== EXBanka Integration Test Runner ===")
	fmt.Println("Running all workflow tests...")

	cmd := exec.Command("go", "test", "-tags", "integration", "./workflows/", "-v", "-count=1", "-timeout=60m")
	cmd.Dir = findModuleRoot()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("\nTests FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nAll tests PASSED!")
}

func findModuleRoot() string {
	// Runner is at cmd/runner/main.go; module root is 2 levels up.
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		dir, _ := os.Getwd()
		return dir
	}
	// filename is the absolute path to this source file.
	// Go up two directories: cmd/runner -> cmd -> module root.
	return filepath.Join(filepath.Dir(filename), "..", "..")
}
