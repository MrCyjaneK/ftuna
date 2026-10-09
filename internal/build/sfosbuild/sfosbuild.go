// Package sfosbuild runs the sfosbuild command in a prepared tree.
package sfosbuild

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func Run(ctx context.Context, version, arches, project, outDir, buildPath string) error {
	bin, err := exec.LookPath("sfosbuild")
	if err != nil {
		return fmt.Errorf("sfosbuild not on PATH")
	}
	if buildPath == "" {
		buildPath = "."
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// Flags have to come before the version. sfosbuild stops parsing at the first positional argument.
	cmd := exec.CommandContext(ctx, bin, "-o", outDir, version, arches, buildPath)
	cmd.Dir = project
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sfosbuild %s %s %s: %w", version, arches, buildPath, err)
	}
	return nil
}
