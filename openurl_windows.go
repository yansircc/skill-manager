//go:build windows

package main

import (
	"fmt"
	"os/exec"
)

func openURL(url string) error {
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run(); err != nil {
		return fmt.Errorf("open dashboard: %w", err)
	}
	return nil
}
