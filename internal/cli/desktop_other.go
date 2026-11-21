//go:build !windows

package cli

// RunDesktop is only used on Windows; elsewhere the CLI entry point is used.
func RunDesktop(self string) {}
