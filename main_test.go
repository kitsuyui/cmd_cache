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
	"time"

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
	if hashString != "b2c91fc952edc927a20d1802dcd650c79b0faa4b" {
		t.Error("Unexpected hash value:", hashString)
	}
}

func TestEnvHashAbsentVsEmptyCollisionPrevented(t *testing.T) {
	// env var set to empty string must produce a different hash than env var not set
	const testKey = "CMD_CACHE_TEST_ABSENT_EMPTY"
	_ = os.Unsetenv(testKey)
	hashNotSet := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{},
		Texts:                    []string{},
		EnvironmentVariableNames: []string{testKey},
		Filenames:                []string{},
	})

	t.Setenv(testKey, "")
	hashSetToEmpty := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{},
		Texts:                    []string{},
		EnvironmentVariableNames: []string{testKey},
		Filenames:                []string{},
	})

	if hashNotSet == hashSetToEmpty {
		t.Error("hash collision: env var set to empty must differ from env var not set")
	}
}

func TestDependencyInputOrderDoesNotAffectHash(t *testing.T) {
	const envA = "CMD_CACHE_TEST_ORDER_A"
	const envB = "CMD_CACHE_TEST_ORDER_B"
	t.Setenv(envA, "a")
	t.Setenv(envB, "b")

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	fileA := "a.txt"
	fileB := "b.txt"
	if err := os.WriteFile(fileA, []byte("file a"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("file b"), 0666); err != nil {
		t.Fatal(err)
	}

	first := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{"sh", "-c", "echo order-sensitive-command"},
		Texts:                    []string{"beta", "alpha"},
		EnvironmentVariableNames: []string{envB, envA},
		Filenames:                []string{fileB, fileA},
	})
	second := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{"sh", "-c", "echo order-sensitive-command"},
		Texts:                    []string{"alpha", "beta"},
		EnvironmentVariableNames: []string{envA, envB},
		Filenames:                []string{fileA, fileB},
	})

	if first != second {
		t.Fatalf("dependency input order changed hash: %s != %s", first, second)
	}
}

func TestCommandOrderStillAffectsHash(t *testing.T) {
	first := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{"echo", "hello"},
		Texts:                    []string{},
		EnvironmentVariableNames: []string{},
		Filenames:                []string{},
	})
	second := hashStringFromCommandContext(t, CommandContext{
		Command:                  []string{"hello", "echo"},
		Texts:                    []string{},
		EnvironmentVariableNames: []string{},
		Filenames:                []string{},
	})

	if first == second {
		t.Fatal("command order must remain part of the hash")
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

func TestFileHashRejectsParentDirectoryTraversal(t *testing.T) {
	h := sha1.New()
	err := writeFileToHash(h, "../outside.txt")
	if err == nil {
		t.Fatal("writeFileToHash accepted a dependency path outside the current working directory")
	}
	if !strings.Contains(err.Error(), "must not escape the current working directory") {
		t.Fatalf("error = %q, want current working directory escape message", err)
	}
}

func TestFileHashRejectsAbsolutePath(t *testing.T) {
	h := sha1.New()
	absolutePath := filepath.Join(t.TempDir(), "dependency.txt")
	err := writeFileToHash(h, absolutePath)
	if err == nil {
		t.Fatal("writeFileToHash accepted an absolute dependency path")
	}
	if !strings.Contains(err.Error(), "must be relative to the current working directory") {
		t.Fatalf("error = %q, want relative path message", err)
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
		commandCache.StatusFilepath: string(formatCachedExitStatus(0)),
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

func TestParseCachedExitStatusAcceptsVersionedStatus(t *testing.T) {
	exitStatus, err := parseCachedExitStatus(formatCachedExitStatus(23))
	if err != nil {
		t.Fatal(err)
	}
	if exitStatus != 23 {
		t.Fatalf("exit status = %d, want 23", exitStatus)
	}
}

func TestParseCachedExitStatusAcceptsLegacyStatus(t *testing.T) {
	exitStatus, err := parseCachedExitStatus([]byte("0"))
	if err != nil {
		t.Fatal(err)
	}
	if exitStatus != 0 {
		t.Fatalf("exit status = %d, want 0", exitStatus)
	}
}

func TestParseCachedExitStatusRejectsUnknownVersionedPayload(t *testing.T) {
	_, err := parseCachedExitStatus([]byte("cmd_cache status v2\n0\n"))
	if err == nil {
		t.Fatal("parseCachedExitStatus accepted an unsupported versioned payload")
	}
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

func TestReplayByCacheNoOutputOnInvalidStatus(t *testing.T) {
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

	stdout, _ := captureStdoutDuring(t, func() {
		_, _ = commandCache.ReplayByCache()
	})
	if stdout != "" {
		t.Fatalf("ReplayByCache() wrote %q to stdout before status validation", stdout)
	}
}

func TestReplayByCacheNoStdoutWhenErrFileMissing(t *testing.T) {
	// When _err is absent the replay must fail without emitting any stdout.
	// Before the buffering fix, io.Copy to stdout succeeded before the
	// subsequent _err open failed, leaving stdout partially replayed and
	// causing RunAndCache to produce a duplicate on fallback.
	cacheDirectory := t.TempDir()
	cacheKey := "cache-key"
	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "echo unreachable"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}

	for path, value := range map[string]string{
		commandCache.StatusFilepath: "0",
		commandCache.OutFilepath:    "cached stdout content",
		// ErrFilepath intentionally absent
	} {
		if err := os.WriteFile(path, []byte(value), 0666); err != nil {
			t.Fatal(err)
		}
	}

	stdout, _ := captureStdoutDuring(t, func() {
		_, _ = commandCache.ReplayByCache()
	})
	if stdout != "" {
		t.Fatalf("ReplayByCache() wrote %q to stdout when err file was missing", stdout)
	}
}

func TestReplayMuxRejectsUnknownStreamID(t *testing.T) {
	// A frame with stream_id other than 1 or 2 must cause replayMux to return an
	// error rather than silently dropping the frame and reporting success. Silent
	// drops cause the caller to treat a corrupt mux file as valid, so the cached
	// output diverges from what the command actually produced.
	cacheDirectory := t.TempDir()
	cacheKey := "mux-unknown-stream-test"
	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "echo unreachable"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
		MuxFilepath:    filepath.Join(cacheDirectory, cacheKey+"_mux"),
	}

	// Build a mux file with one frame that has an unknown stream_id (3).
	unknownData := []byte("dropped-data")
	var muxData []byte
	muxData = append(muxData, 3, 0, 0, 0, byte(len(unknownData))) // stream_id=3 (invalid)
	muxData = append(muxData, unknownData...)

	for path, content := range map[string][]byte{
		commandCache.StatusFilepath: []byte("0"),
		commandCache.OutFilepath:    []byte("cached stdout"),
		commandCache.ErrFilepath:    []byte("cached stderr"),
		commandCache.MuxFilepath:    muxData,
	} {
		if err := os.WriteFile(path, content, 0666); err != nil {
			t.Fatal(err)
		}
	}

	_, err := commandCache.ReplayByCache()
	if err == nil {
		t.Fatal("ReplayByCache() returned nil error for unknown stream_id in mux file")
	}
	if !strings.Contains(err.Error(), "unknown stream_id") {
		t.Fatalf("error %q does not mention unknown stream_id", err.Error())
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

func TestRunAndCacheCreatesMuxFileAndReplayPreservesStreams(t *testing.T) {
	cacheDirectory := t.TempDir()
	cacheKey := "mux-test"
	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "printf out; printf err >&2"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
		MuxFilepath:    filepath.Join(cacheDirectory, cacheKey+"_mux"),
	}

	if _, err := commandCache.RunAndCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(commandCache.MuxFilepath); err != nil {
		t.Fatal("mux file must exist after RunAndCache:", err)
	}
	assertNoCacheTempFiles(t, cacheDirectory)

	// ReplayByCache must use the mux file and deliver correct stdout/stderr content.
	stdout, _ := captureStdoutDuring(t, func() {
		stderr, _ := captureStderrDuring(t, func() {
			if _, err := commandCache.ReplayByCache(); err != nil {
				t.Errorf("ReplayByCache failed: %v", err)
			}
		})
		if stderr != "err" {
			t.Errorf("stderr = %q, want %q", stderr, "err")
		}
	})
	if stdout != "out" {
		t.Errorf("stdout = %q, want %q", stdout, "out")
	}
}

func TestReplayByCacheFallsBackToSequentialWithoutMuxFile(t *testing.T) {
	cacheDirectory := t.TempDir()
	cacheKey := "no-mux-test"
	commandCache := CommandCache{
		Command:        []string{"sh", "-c", "printf old-out; printf old-err >&2"},
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
		MuxFilepath:    filepath.Join(cacheDirectory, cacheKey+"_mux"),
	}

	// Write old-format cache files (no mux file).
	for path, content := range map[string]string{
		commandCache.StatusFilepath: "0",
		commandCache.OutFilepath:    "old-out",
		commandCache.ErrFilepath:    "old-err",
	} {
		if err := os.WriteFile(path, []byte(content), 0666); err != nil {
			t.Fatal(err)
		}
	}

	// ReplayByCache must fall back to stdout-then-stderr without error.
	exitStatus, err := commandCache.ReplayByCache()
	if err != nil {
		t.Fatal("ReplayByCache failed on old-format cache:", err)
	}
	if exitStatus != 0 {
		t.Fatalf("unexpected exit status: %d", exitStatus)
	}
}

func TestParseMaxCacheEntries(t *testing.T) {
	for input, expected := range map[string]int{
		"0":    0,
		"1":    1,
		"1024": 1024,
	} {
		actual, err := parseMaxCacheEntries(input)
		if err != nil {
			t.Fatalf("parseMaxCacheEntries(%q) returned error: %v", input, err)
		}
		if actual != expected {
			t.Fatalf("parseMaxCacheEntries(%q) = %d, want %d", input, actual, expected)
		}
	}

	for _, input := range []string{"", "-1", "abc"} {
		if _, err := parseMaxCacheEntries(input); err == nil {
			t.Fatalf("parseMaxCacheEntries(%q) returned nil error", input)
		}
	}
}

func TestIsCacheKey(t *testing.T) {
	validKey := "0123456789abcdef0123456789abcdef01234567"
	if !isCacheKey(validKey) {
		t.Fatalf("isCacheKey(%q) = false, want true", validKey)
	}

	for _, input := range []string{
		"short",
		"0123456789abcdef0123456789abcdef0123456g",
		"0123456789ABCDEF0123456789abcdef01234567",
		"cache-key",
	} {
		if isCacheKey(input) {
			t.Fatalf("isCacheKey(%q) = true, want false", input)
		}
	}
}

func TestMainRejectsInvalidMaxCacheEntries(t *testing.T) {
	originalExit := exit
	defer func() {
		exit = originalExit
	}()
	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	os.Args = []string{"cmd_cache", "--max-cache-entries=-1", "--", "sh", "-c", "echo should-not-run"}
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
	if !strings.Contains(output, "--max-cache-entries must be a non-negative integer") {
		t.Fatalf("stderr = %q, want max-cache-entries validation error", output)
	}
}

func TestPruneCacheEntriesRemovesOldestCompleteEntries(t *testing.T) {
	cacheDirectory := t.TempDir()
	oldTime := time.Unix(100, 0)
	middleTime := time.Unix(200, 0)
	newTime := time.Unix(300, 0)
	oldKey := "0000000000000000000000000000000000000001"
	middleKey := "0000000000000000000000000000000000000002"
	newKey := "0000000000000000000000000000000000000003"

	writeCompleteCacheEntry(t, cacheDirectory, oldKey, oldTime)
	writeCompleteCacheEntry(t, cacheDirectory, middleKey, middleTime)
	writeCompleteCacheEntry(t, cacheDirectory, newKey, newTime)

	if err := pruneCacheEntries(cacheDirectory, 2); err != nil {
		t.Fatal(err)
	}

	assertCacheEntryRemoved(t, cacheDirectory, oldKey)
	assertCacheEntryExists(t, cacheDirectory, middleKey)
	assertCacheEntryExists(t, cacheDirectory, newKey)

	entries, err := collectCompleteCacheEntries(cacheDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("complete entries = %d, want 2", len(entries))
	}
}

func TestPruneCacheEntriesIgnoresIncompleteEntries(t *testing.T) {
	cacheDirectory := t.TempDir()
	oldCompleteKey := "0000000000000000000000000000000000000001"
	newCompleteKey := "0000000000000000000000000000000000000002"
	incompleteKey := "0000000000000000000000000000000000000003"
	writeCompleteCacheEntry(t, cacheDirectory, oldCompleteKey, time.Unix(100, 0))
	writeCompleteCacheEntry(t, cacheDirectory, newCompleteKey, time.Unix(300, 0))
	writePartialCacheEntry(t, cacheDirectory, incompleteKey, time.Unix(1, 0))

	if err := pruneCacheEntries(cacheDirectory, 1); err != nil {
		t.Fatal(err)
	}

	assertCacheEntryRemoved(t, cacheDirectory, oldCompleteKey)
	assertCacheEntryExists(t, cacheDirectory, newCompleteKey)
	assertPartialCacheEntryExists(t, cacheDirectory, incompleteKey)
}

func TestPruneCacheEntriesDisabledKeepsCompleteEntries(t *testing.T) {
	cacheDirectory := t.TempDir()
	oldKey := "0000000000000000000000000000000000000001"
	newKey := "0000000000000000000000000000000000000002"
	writeCompleteCacheEntry(t, cacheDirectory, oldKey, time.Unix(100, 0))
	writeCompleteCacheEntry(t, cacheDirectory, newKey, time.Unix(200, 0))

	if err := pruneCacheEntries(cacheDirectory, 0); err != nil {
		t.Fatal(err)
	}

	assertCacheEntryExists(t, cacheDirectory, oldKey)
	assertCacheEntryExists(t, cacheDirectory, newKey)
}

func TestPruneCacheEntriesIgnoresNonCacheFileGroups(t *testing.T) {
	cacheDirectory := t.TempDir()
	oldKey := "0000000000000000000000000000000000000001"
	newKey := "0000000000000000000000000000000000000002"
	writeCompleteCacheEntry(t, cacheDirectory, oldKey, time.Unix(100, 0))
	writeCompleteCacheEntry(t, cacheDirectory, newKey, time.Unix(200, 0))
	writeCompleteCacheEntry(t, cacheDirectory, "not-a-cache-key", time.Unix(1, 0))

	if err := pruneCacheEntries(cacheDirectory, 1); err != nil {
		t.Fatal(err)
	}

	assertCacheEntryRemoved(t, cacheDirectory, oldKey)
	assertCacheEntryExists(t, cacheDirectory, newKey)
	assertCacheEntryExists(t, cacheDirectory, "not-a-cache-key")
}

func writeCompleteCacheEntry(t *testing.T, cacheDirectory, key string, modTime time.Time) {
	t.Helper()

	for suffix, content := range map[string]string{
		"":      "0",
		"_out":  "stdout",
		"_err":  "stderr",
		"_mux":  "mux",
		".lock": "",
	} {
		path := filepath.Join(cacheDirectory, key+suffix)
		if err := os.WriteFile(path, []byte(content), 0666); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
}

func writePartialCacheEntry(t *testing.T, cacheDirectory, key string, modTime time.Time) {
	t.Helper()

	for suffix, content := range map[string]string{
		"":     "0",
		"_out": "stdout",
	} {
		path := filepath.Join(cacheDirectory, key+suffix)
		if err := os.WriteFile(path, []byte(content), 0666); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
}

func assertCacheEntryExists(t *testing.T, cacheDirectory, key string) {
	t.Helper()

	for _, suffix := range []string{"", "_out", "_err", "_mux", ".lock"} {
		if _, err := os.Stat(filepath.Join(cacheDirectory, key+suffix)); err != nil {
			t.Fatalf("cache entry %s%s should exist: %v", key, suffix, err)
		}
	}
}

func assertCacheEntryRemoved(t *testing.T, cacheDirectory, key string) {
	t.Helper()

	for _, suffix := range []string{"", "_out", "_err", "_mux", ".lock"} {
		if _, err := os.Stat(filepath.Join(cacheDirectory, key+suffix)); !os.IsNotExist(err) {
			t.Fatalf("cache entry %s%s should be removed: %v", key, suffix, err)
		}
	}
}

func assertPartialCacheEntryExists(t *testing.T, cacheDirectory, key string) {
	t.Helper()

	for _, suffix := range []string{"", "_out"} {
		if _, err := os.Stat(filepath.Join(cacheDirectory, key+suffix)); err != nil {
			t.Fatalf("partial cache entry %s%s should exist: %v", key, suffix, err)
		}
	}
	for _, suffix := range []string{"_err", "_mux", ".lock"} {
		if _, err := os.Stat(filepath.Join(cacheDirectory, key+suffix)); !os.IsNotExist(err) {
			t.Fatalf("partial cache entry %s%s should not exist: %v", key, suffix, err)
		}
	}
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
