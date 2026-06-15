// entire-judge is an Entire CLI external command.
//
// Once built as an executable named `entire-judge`, the parent Entire CLI
// dispatches it when a user runs `entire judge`. It scores submissions using
// each team's local Entire brain (exported sessions, durable facts, and commit
// history) with deterministic metrics plus optional LLM "judge lenses".
package main

import (
	"fmt"
	"os"

	"github.com/suhaanthayyil/entire-judge/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Execute(version, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
