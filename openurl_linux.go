//go:build linux

package main

import (
	"fmt"
	"os/exec"
)

func openURL(url string) error {
	if err := exec.Command("xdg-open", url).Run(); err != nil {
		return fmt.Errorf("open dashboard: %w", err)
	}
	return nil
}
