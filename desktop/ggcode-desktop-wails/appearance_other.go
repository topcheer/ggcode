//go:build !darwin && !windows && !linux

package main

func detectMacDarkMode() bool { return false }
