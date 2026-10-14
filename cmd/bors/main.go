package main

import (
	"os"

	"github.com/kristyancarvalho/bors/internal/cli"
	"github.com/kristyancarvalho/bors/internal/terminal"
)

var version = "dev"

func main() {
	inputTTY := terminal.IsTerminal(os.Stdin)
	outputTTY := terminal.IsTerminal(os.Stdout)

	code := cli.Run(os.Args[1:], cli.Runtime{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		InputTTY:  inputTTY,
		OutputTTY: outputTTY,
		GetSize:   func() (terminal.Size, error) { return terminal.GetSize(os.Stdout) },
		Probe:     func() (bool, error) { return terminal.ProbeKitty(os.Stdin, os.Stdout, terminal.DefaultProbeTimeout) },
		Env:       os.Environ(),
	}, version)
	os.Exit(code)
}
