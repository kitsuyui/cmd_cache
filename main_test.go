package main

import (
	"crypto/sha1"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	docopt "github.com/docopt/docopt-go"
)

func TestMain(t *testing.T) {
	originalExit := exit
	defer func() { exit = originalExit }()
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	exit = func(i int) {
		if i != 0 {
			t.Error("exit code must be 0")
		}
	}
	cacheDir := t.TempDir()
	os.Args = []string{"cmd_cache", "--cache-directory=" + cacheDir, "--text", "something", "--", "ls", "-ahl"}
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

func TestMainWritesVersionToStdout(t *testing.T) {
	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()
	originalVersion := version
	defer func() {
		version = originalVersion
	}()

	version = "1.2.3-test"
	os.Args = []string{"cmd_cache", "--version"}

	output, recovered := captureStdoutDuring(t, main)
	if recovered != nil {
		t.Fatalf("main() recovered %v, want nil", recovered)
	}
	if output != "1.2.3-test\n" {
		t.Fatalf("stdout = %q, want %q", output, "1.2.3-test\n")
	}
}

type testExitCode int

func captureStdoutDuring(t *testing.T, fn func()) (string, any) {
	t.Helper()

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer

	var recovered any
	func() {
		defer func() {
			recovered = recover()
			os.Stdout = originalStdout
			if err := writer.Close(); err != nil {
				t.Errorf("failed to close stdout writer: %v", err)
			}
		}()
		fn()
	}()

	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return string(output), recovered
}

func captureStderrDuring(t *testing.T, fn func()) (string, any) {
	t.Helper()

	originalStderr := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	log.SetOutput(writer)

	var recovered any
	func() {
		defer func() {
			recovered = recover()
			os.Stderr = originalStderr
			log.SetOutput(originalStderr)
			if err := writer.Close(); err != nil {
				t.Errorf("failed to close stderr writer: %v", err)
			}
		}()
		fn()
	}()

	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return string(output), recovered
}

func TestMainExitsWhenCacheDirectoryCannotBeCreated(t *testing.T) {
	originalExit := exit
	defer func() {
		exit = originalExit
	}()
	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	parentFile := filepath.Join(t.TempDir(), "not-dir")
	if err := os.WriteFile(parentFile, []byte("not a directory"), 0666); err != nil {
		t.Fatal(err)
	}
	cacheDirectory := filepath.Join(parentFile, "cache")
	os.Args = []string{"cmd_cache", "--cache-directory=" + cacheDirectory, "--", "sh", "-c", "echo should-not-run"}
	exit = func(code int) {
		panic(testExitCode(code))
	}

	output, recovered := captureStderrDuring(t, main)
	code, ok := recovered.(testExitCode)
	if !ok {
		t.Fatalf("main() recovered %v, want exit code panic", recovered)
	}
	if code != 1 {
		t.Fatalf("unexpected exit code: %d", code)
	}
	if !strings.Contains(output, parentFile) {
		t.Fatalf("stderr = %q, want mkdir error mentioning %q", output, parentFile)
	}
}

func hashStringFromCommandContext(t *testing.T, cc CommandContext) string {
	t.Helper()
	h := sha1.New()
	if err := cc.WriteToHash(h); err != nil {
		t.Fatalf("WriteToHash failed: %v", err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestHashWhenEverythingIsEmpty(t *testing.T) {
	hashString := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{},
		Texts:                    []string{},
		EnvironmentVariableNames: []string{},
		Filenames:                []string{},
	})
	if hashString != "28d86c56b3bf26d236569b8dc8c3f91f32f47bc7" {
		t.Error("Unexpected hash value:", hashString)
	}
}

func TestTextHash(t *testing.T) {
	hashString := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{},
		Texts:                    []string{"Hello, World!"},
		EnvironmentVariableNames: []string{},
		Filenames:                []string{},
	})
	if hashString != "cd5a4bda291ed743b9a4bf3e0e9109d7f83b0fe2" {
		t.Error("Unexpected hash value:", hashString)
	}
}

func TestEnvHash(t *testing.T) {
	t.Setenv("LD_LIBRARY_PATH", "/usr/local/lib:/usr/lib")
	hashString := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{},
		Texts:                    []string{},
		EnvironmentVariableNames: []string{"LD_LIBRARY_PATH"},
		Filenames:                []string{},
	})
	if hashString != "8475fc388c3fe8f6d3c5b52ab71f8476fff1c1b5" {
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
	hashString := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{},
		Texts:                    []string{},
		EnvironmentVariableNames: []string{},
		Filenames:                []string{"build/testfile"},
	})
	if hashString != "6d7e43ef5351becf7a79030cfa7325756176674b" {
		t.Error("Unexpected hash value:", hashString)
	}
}

func TestHashCollisionPrevented(t *testing.T) {
	// "echo" command with "foo" text must differ from "echofoo" command alone.
	hashCmdAndText := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{"echo"},
		Texts:                    []string{"foo"},
		EnvironmentVariableNames: []string{},
		Filenames:                []string{},
	})
	hashCmdOnly := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{"echofoo"},
		Texts:                    []string{},
		EnvironmentVariableNames: []string{},
		Filenames:                []string{},
	})
	if hashCmdAndText == hashCmdOnly {
		t.Error("hash collision: {cmd:[echo], text:[foo]} must differ from {cmd:[echofoo]}")
	}
}

func TestSomething(t *testing.T) {
	cacheDirectory := t.TempDir()
	cacheKey := "cache-key"
	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "echo 1"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}
	if _, err := commandCache.RunAndCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := commandCache.ReplayByCache(); err != nil {
		t.Fatal(err)
	}
}

func TestGetOrRunExecutesAndCachesOnMiss(t *testing.T) {
	cacheDirectory := t.TempDir()
	cacheKey := "cache-key"
	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "echo hello"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}

	exitStatus, err := commandCache.GetOrRun()
	if err != nil {
		t.Fatal(err)
	}
	if exitStatus != 0 {
		t.Fatalf("unexpected exit status: %d", exitStatus)
	}
	if _, err := os.Stat(commandCache.StatusFilepath); err != nil {
		t.Fatalf("status file must exist after GetOrRun: %v", err)
	}
}

func TestGetOrRunReplaysCacheOnHit(t *testing.T) {
	cacheDirectory := t.TempDir()
	cacheKey := "cache-key"
	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "echo cached"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}

	// First call: execute and cache.
	if _, err := commandCache.GetOrRun(); err != nil {
		t.Fatal(err)
	}

	// Second call: cache hit via double-check path (lock acquired, cache found).
	exitStatus, err := commandCache.GetOrRun()
	if err != nil {
		t.Fatal(err)
	}
	if exitStatus != 0 {
		t.Fatalf("unexpected exit status on cache hit: %d", exitStatus)
	}
}

func TestRunAndCacheTruncatesExistingCacheFiles(t *testing.T) {
	cacheDirectory := ".cmd_cache_test"
	cacheKey := "cache-key"
	if err := os.MkdirAll(cacheDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDirectory)

	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "printf x; printf y >&2"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}

	for path, value := range map[string]string{
		commandCache.StatusFilepath: "123456789",
		commandCache.OutFilepath:    "previous stdout",
		commandCache.ErrFilepath:    "previous stderr",
	} {
		if err := os.WriteFile(path, []byte(value), 0666); err != nil {
			t.Fatal(err)
		}
	}

	exitStatus, err := commandCache.RunAndCache()
	if err != nil {
		t.Fatal(err)
	}
	if exitStatus != 0 {
		t.Fatalf("unexpected exit status: %d", exitStatus)
	}

	for path, expected := range map[string]string{
		commandCache.StatusFilepath: "0",
		commandCache.OutFilepath:    "x",
		commandCache.ErrFilepath:    "y",
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != expected {
			t.Fatalf("%s = %q, want %q", path, string(content), expected)
		}
	}
	assertNoCacheTempFiles(t, cacheDirectory)
}

func TestRunAndCacheDoesNotCacheFailures(t *testing.T) {
	cacheDirectory := t.TempDir()
	cacheKey := "cache-key"
	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "printf x; printf y >&2; exit 7"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}

	exitStatus, err := commandCache.RunAndCache()
	if err != nil {
		t.Fatal(err)
	}
	if exitStatus != 7 {
		t.Fatalf("unexpected exit status: %d", exitStatus)
	}
	if _, err := os.Stat(commandCache.StatusFilepath); !os.IsNotExist(err) {
		t.Fatal("status file must not be written for failed commands")
	}
	assertNoCacheTempFiles(t, cacheDirectory)
}

func TestReplayByCacheRejectsInvalidStatus(t *testing.T) {
	cacheDirectory := t.TempDir()
	cacheKey := "cache-key"
	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "echo unreachable"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}

	for path, value := range map[string]string{
		commandCache.StatusFilepath: "not-a-status",
		commandCache.OutFilepath:    "cached stdout",
		commandCache.ErrFilepath:    "cached stderr",
	} {
		if err := os.WriteFile(path, []byte(value), 0666); err != nil {
			t.Fatal(err)
		}
	}

	_, err := commandCache.ReplayByCache()
	if err == nil {
		t.Fatal("ReplayByCache() returned nil error for invalid status")
	}
	msg := err.Error()
	if !strings.Contains(msg, commandCache.StatusFilepath) {
		t.Errorf("error message %q does not contain status filepath %q", msg, commandCache.StatusFilepath)
	}
	if !strings.Contains(msg, "not-a-status") {
		t.Errorf("error message %q does not contain invalid status content", msg)
	}
}

func TestRunAndCacheReturnsCommandStartError(t *testing.T) {
	cacheDirectory := t.TempDir()
	cacheKey := "cache-key"
	commandCache := CommandCache{
		Command:        []string{"cmd-cache-command-that-does-not-exist"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}

	exitStatus, err := commandCache.RunAndCache()
	if err == nil {
		t.Fatal("RunAndCache() returned nil error for command start failure")
	}
	if exitStatus != 1 {
		t.Fatalf("unexpected exit status: %d", exitStatus)
	}
	if _, err := os.Stat(commandCache.StatusFilepath); !os.IsNotExist(err) {
		t.Fatalf("status file should not be cached for command start failure: %v", err)
	}
	assertNoCacheTempFiles(t, cacheDirectory)
}

func assertNoCacheTempFiles(t *testing.T, cacheDirectory string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(cacheDirectory, ".*.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary cache files were not cleaned up: %v", matches)
	}
}
