// Package main — omnish entry point.
package main

import "os"

// buildVersion is overridden at build time via -ldflags "-X main.buildVersion=<tag>".
var buildVersion = "v0.1.0"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
