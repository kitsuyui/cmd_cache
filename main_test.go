package main

import (
	"testing"
)

func TestMain(t *testing.T) {
	exit = func(i int) {
		if i != 1 {
			t.Error("exit code Must be 1")
		}
	}
	main()
}
