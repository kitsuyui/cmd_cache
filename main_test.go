package main

import (
	"crypto/sha1"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"testing"

	docopt "github.com/docopt/docopt-go"
)

func TestMain(t *testing.T) {
	exit = func(i int) {
		if i != 0 {
			t.Error("exit code must be 0")
		}
	}
	if err := os.RemoveAll(".cmd_cache"); err != nil {
		panic(err)
	}
	os.Args = []string{"cmd_cache", "--text", "something", "--", "ls", "-ahl"}
	// without cache
	main()
	// with cache
	main()
}

func TestDocopt(t *testing.T) {
	_, err := docopt.ParseArgs(COMMAND_USAGE, []string{
		"--file", "test.txt", "--", "ls", "-ahl",
	}, "")
	if err != nil {
		t.Error("Document for docopt has broken")
	}
}

func TestVersion(t *testing.T) {
	_, err := docopt.ParseArgs(COMMAND_USAGE, []string{
		"--version",
	}, "")
	if err != nil {
		t.Error("Document for docopt has broken")
	}
}

func hashStringFromCommandContext(cc CommandContext) string {
	h := sha1.New()
	cc.WriteToHash(h)
	hashString := hex.EncodeToString(h.Sum(nil))
	return hashString
}

func TestHashWhenEverythingIsEmpty(t *testing.T) {
	hashString := hashStringFromCommandContext(CommandContext{
		Command: []string{},
		Texts:   []string{},
		EnvironmentVariableNames: []string{},
		Filenames:                []string{},
	})
	if hashString != "da39a3ee5e6b4b0d3255bfef95601890afd80709" {
		t.Error("Unexpected hash value:", hashString)
	}
}

func TestTextHash(t *testing.T) {
	hashString := hashStringFromCommandContext(CommandContext{
		Command: []string{},
		Texts:   []string{"Hello, World!"},
		EnvironmentVariableNames: []string{},
		Filenames:                []string{},
	})
	if hashString != "0a0a9f2a6772942557ab5355d76af442f8f65e01" {
		t.Error("Unexpected hash value:", hashString)
	}
}

func TestEnvHash(t *testing.T) {
	os.Setenv("LD_LIBRARY_PATH", "/usr/local/lib:/usr/lib")
	hashString := hashStringFromCommandContext(CommandContext{
		Command: []string{},
		Texts:   []string{},
		EnvironmentVariableNames: []string{"LD_LIBRARY_PATH"},
		Filenames:                []string{},
	})
	if hashString != "3d0fcf9d8dac962ba44dae6b205b541075451732" {
		t.Error("Unexpected hash value:", hashString)
	}
}

func TestFileHash(t *testing.T) {
	outFile, err := os.OpenFile("build/testfile", os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		log.Fatal(err)
	}
	outFile.Write([]byte("Hello, World!"))
	defer outFile.Close()
	hashString := hashStringFromCommandContext(CommandContext{
		Command: []string{},
		Texts:   []string{},
		EnvironmentVariableNames: []string{},
		Filenames:                []string{"build/testfile"},
	})
	if hashString != "8bfe40a6a2f5765f8e1d148bfb3d39d4fa0e709a" {
		t.Error("Unexpected hash value:", hashString)
	}
}

func TestSomething(t *testing.T) {
	cacheDirectory := ".cmd_cache"
	cacheKey := "cache-key"
	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "echo 1"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}
	commandCache.RunAndCache()
	commandCache.ReplayByCache()
}
