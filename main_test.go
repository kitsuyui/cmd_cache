package main

import (
	"testing"

	docopt "github.com/docopt/docopt-go"
)

func TestDocopt(t *testing.T) {
	_, err := docopt.ParseArgs(COMMAND_USAGE, []string{
		"--file", "test.txt", "--", "ls", "-ahl",
	}, "")
	if err != nil {
		t.Error("Document for docopt has broken")
	}
}
