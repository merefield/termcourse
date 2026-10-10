//go:build !darwin

package ui

func appleTerminalSupportsTrueColor() bool { return false }
