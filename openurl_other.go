//go:build !darwin && !linux && !windows

package skillmanager

import "fmt"

func openURL(string) error {
	return fmt.Errorf("opening a browser is unsupported on this platform; use sm dashboard")
}
