package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/jaydoubleu/bothy/internal/cli"
	"github.com/jaydoubleu/bothy/internal/runtime"
)

func main() {
	err := cli.Execute()
	if err == nil {
		return
	}
	// A command run inside a bothy already printed its own errors; just
	// propagate its exit code.
	var exitErr *runtime.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.Code)
	}
	fmt.Fprintln(os.Stderr, "bothy: "+err.Error())
	os.Exit(1)
}
