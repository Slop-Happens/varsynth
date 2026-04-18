package main

import (
	"fmt"
	"os"

	"github.com/Slop-Happens/varsynth/cmd/varsynth/internal/app"
)

// main forwards CLI execution to the application layer and exits on failure.
func main() {
	if err := app.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
