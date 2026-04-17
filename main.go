package main

import (
	"fmt"
	"os"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get home directory: %v\n", err)
		os.Exit(1)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize: %v\n", err)
		os.Exit(1)
	}

	p := newProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "application error: %v\n", err)
		os.Exit(1)
	}
}
