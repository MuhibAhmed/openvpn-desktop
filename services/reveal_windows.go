//go:build windows

package services

import (
	"os"
	"os/exec"
)

// revealInFileManager opens dir in Explorer, creating it first so the user is
// not told a folder we own does not exist.
func revealInFileManager(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// explorer.exe returns a non-zero exit code even when it succeeds, so the
	// error is deliberately not checked.
	_ = exec.Command("explorer.exe", dir).Start()
	return nil
}
