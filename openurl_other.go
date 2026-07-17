//go:build !darwin && !linux && !windows

package main

import "fmt"

func openURL(string) error {
	return fmt.Errorf("opening a browser is unsupported on this platform; use sm dashboard")
}
