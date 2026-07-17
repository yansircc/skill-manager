//go:build darwin

package skillmanager

import (
	"fmt"
	"os/exec"
)

func openURL(url string) error {
	if err := exec.Command("open", url).Run(); err != nil {
		return fmt.Errorf("open dashboard: %w", err)
	}
	return nil
}
