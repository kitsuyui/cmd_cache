package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/docopt/docopt-go"
)

const COMMAND_USAGE = `cmd_cache
Usage:
 cmd_cache [--cache-directory=DIRECTORY] [(--file FILE | --env ENV | --text TEXT)...] -- [COMMAND...]
 cmd_cache (--help | --version)

Arguments:
 FILE      depending file. (e.g. prog.h)
 ENV       depending environment variable. (e.g. LD_LIBRARY_PATH)
 TEXT      text affecting command.
 COMMAND   real command.

Options:
 -h --help               						 Show this screen.
 -V --version            						 Show version.
 --cache-directory=DIRECTORY    Cache directory [default: .cmd_cache]
`

type CommandContext struct {
	Command                  []string
	Texts                    []string
	EnvironmentVariableNames []string
	Filenames                []string
}

func (cc CommandContext) WriteToHash(h hash.Hash) error {
	for _, cmd := range cc.Command {
		io.WriteString(h, cmd)
		io.WriteString(h, "\x00")
	}
	io.WriteString(h, "\x01")
	for _, text := range cc.Texts {
		io.WriteString(h, text)
		io.WriteString(h, "\x00")
	}
	io.WriteString(h, "\x01")
	for _, filename := range cc.Filenames {
		if err := writeFileToHash(h, filename); err != nil {
			return err
		}
	}
	io.WriteString(h, "\x01")
	for _, envname := range cc.EnvironmentVariableNames {
		io.WriteString(h, envname)
		io.WriteString(h, "\x00")
		if value, ok := os.LookupEnv(envname); ok {
			io.WriteString(h, value)
		}
		io.WriteString(h, "\x00")
	}
	return nil
}

func writeFileToHash(h hash.Hash, filename string) error {
	io.WriteString(h, filename)
	io.WriteString(h, "\x00")
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	io.WriteString(h, "\x01")
	return nil
}

type CommandCache struct {
	Command        []string
	StatusFilepath string
	OutFilepath    string
	ErrFilepath    string
}

type cacheTempFile struct {
	file      *os.File
	path      string
	finalPath string
}

func newCacheTempFile(finalPath string) (*cacheTempFile, error) {
	file, err := os.CreateTemp(filepath.Dir(finalPath), "."+filepath.Base(finalPath)+".tmp-")
	if err != nil {
		return nil, err
	}
	return &cacheTempFile{
		file:      file,
		path:      file.Name(),
		finalPath: finalPath,
	}, nil
}

func (f *cacheTempFile) Close() error {
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}

func (f *cacheTempFile) Remove() {
	_ = f.Close()
	if f.path == "" {
		return
	}
	_ = os.Remove(f.path)
	f.path = ""
}

func (f *cacheTempFile) Rename() error {
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.path, f.finalPath); err != nil {
		return err
	}
	f.path = ""
	return nil
}

// GetOrRun returns the cached result if available. On a cache miss it acquires
// an exclusive advisory lock on a per-cache-key lock file, re-checks the cache
// (another process may have populated it while we waited), and runs the command
// only if the cache is still empty. This prevents multiple concurrent processes
// from executing the same command (thundering herd / stampede).
func (cc CommandCache) GetOrRun() (int, error) {
	// Fast path: read from cache without acquiring a lock.
	if exitStatus, err := cc.ReplayByCache(); err == nil {
		return exitStatus, nil
	}

	lockPath := cc.StatusFilepath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		// Lock file cannot be opened; fall back to running without a lock.
		return cc.RunAndCache()
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		// flock failed; fall back to running without a lock.
		return cc.RunAndCache()
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	// Double-check: another process may have written the cache while we waited.
	if exitStatus, err := cc.ReplayByCache(); err == nil {
		return exitStatus, nil
	}

	return cc.RunAndCache()
}

func (cc CommandCache) ReplayByCache() (int, error) {
	outFile, err := os.Open(cc.OutFilepath)
	if err != nil {
		return 0, err
	}
	defer outFile.Close()
	errFile, err := os.Open(cc.ErrFilepath)
	if err != nil {
		return 0, err
	}
	defer errFile.Close()
	statusFile, err := os.Open(cc.StatusFilepath)
	if err != nil {
		return 0, err
	}
	defer statusFile.Close()
	exitStatusText, err := io.ReadAll(statusFile)
	if err != nil {
		return 0, err
	}
	exitStatus, err := strconv.Atoi(string(exitStatusText))
	if err != nil {
		return 0, fmt.Errorf("invalid cached exit status in %s (%q): %w", cc.StatusFilepath, string(exitStatusText), err)
	}
	if _, err := io.Copy(os.Stdout, outFile); err != nil {
		return 0, err
	}
	if _, err := io.Copy(os.Stderr, errFile); err != nil {
		return 0, err
	}
	return exitStatus, nil
}

func (cc CommandCache) RunAndCache() (int, error) {
	outFile, err := newCacheTempFile(cc.OutFilepath)
	if err != nil {
		return 1, err
	}
	defer outFile.Remove()
	errFile, err := newCacheTempFile(cc.ErrFilepath)
	if err != nil {
		return 1, err
	}
	defer errFile.Remove()
	outWriter := io.MultiWriter(outFile.file, os.Stdout)
	errWriter := io.MultiWriter(errFile.file, os.Stderr)

	commands := cc.Command
	cmd := exec.Command(commands[0], commands[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdout = outWriter
	cmd.Stderr = errWriter
	err = cmd.Run()
	var exitStatus int
	if err2, ok := err.(*exec.ExitError); ok {
		if s, ok := err2.Sys().(syscall.WaitStatus); ok {
			exitStatus = s.ExitStatus()
		}
	} else if err != nil {
		return 1, err
	}
	if exitStatus != 0 {
		// Don't cache failures; the command should be retried on the next invocation.
		return exitStatus, nil
	}
	statusFile, err := newCacheTempFile(cc.StatusFilepath)
	if err != nil {
		return exitStatus, err
	}
	defer statusFile.Remove()
	if _, err := statusFile.file.Write([]byte(strconv.Itoa(exitStatus))); err != nil {
		return exitStatus, err
	}
	if err := os.Remove(cc.StatusFilepath); err != nil && !os.IsNotExist(err) {
		return exitStatus, err
	}
	if err := outFile.Rename(); err != nil {
		return exitStatus, err
	}
	if err := errFile.Rename(); err != nil {
		return exitStatus, err
	}
	if err := statusFile.Rename(); err != nil {
		return exitStatus, err
	}
	return exitStatus, nil
}

var version string
var exit = os.Exit

func main() {
	opts, err := docopt.ParseDoc(COMMAND_USAGE)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
	if showVersion, _ := opts.Bool("--version"); showVersion {
		fmt.Println(version)
		return
	}
	cacheDirectory := opts["--cache-directory"].(string)
	if err := os.MkdirAll(cacheDirectory, 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}

	commands := opts["COMMAND"].([]string)
	commandContext := CommandContext{
		Command:                  commands,
		Texts:                    opts["TEXT"].([]string),
		EnvironmentVariableNames: opts["ENV"].([]string),
		Filenames:                opts["FILE"].([]string),
	}

	h := sha1.New()
	if err := commandContext.WriteToHash(h); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
	cacheKey := hex.EncodeToString(h.Sum(nil))

	commandCache := CommandCache{
		Command:        commands,
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}

	exitStatus, err := commandCache.GetOrRun()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	exit(exitStatus)
}
