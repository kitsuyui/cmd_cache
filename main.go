package main

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/docopt/docopt-go"
)

const COMMAND_USAGE = `cmd_cache
Usage:
 cmd_cache [--cache-directory=DIRECTORY] [(--file FILE | --env ENV | --text TEXT)...] -- [COMMAND...]
 cmd_cache (--help | --version)

Arguments:
 FILE      depending file under the current working directory. (e.g. prog.h)
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
	for _, text := range sortedStrings(cc.Texts) {
		io.WriteString(h, text)
		io.WriteString(h, "\x00")
	}
	io.WriteString(h, "\x01")
	for _, filename := range sortedStrings(cc.Filenames) {
		if err := writeFileToHash(h, filename); err != nil {
			return err
		}
	}
	io.WriteString(h, "\x01")
	for _, envname := range sortedStrings(cc.EnvironmentVariableNames) {
		io.WriteString(h, envname)
		io.WriteString(h, "\x00")
		if value, ok := os.LookupEnv(envname); ok {
			io.WriteString(h, "\x02") // present marker: distinguishes set-to-empty from not-set
			io.WriteString(h, value)
		}
		io.WriteString(h, "\x00")
	}
	return nil
}

func sortedStrings(values []string) []string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	return sorted
}

func writeFileToHash(h hash.Hash, filename string) error {
	if err := validateDependencyFilename(filename); err != nil {
		return err
	}
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

func validateDependencyFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("dependency file path must not be empty")
	}
	clean := filepath.Clean(filename)
	if filepath.IsAbs(clean) {
		return fmt.Errorf("dependency file path %q must be relative to the current working directory", filename)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("dependency file path %q must not escape the current working directory", filename)
	}
	return nil
}

type CommandCache struct {
	Command        []string
	StatusFilepath string
	OutFilepath    string
	ErrFilepath    string
	// MuxFilepath stores an interleaved stdout/stderr stream so ReplayByCache can
	// reproduce the original write order instead of replaying all stdout then all
	// stderr. Empty string disables mux writing and falls back to sequential replay.
	MuxFilepath string
}

// muxWriter writes framed chunks to a shared multiplexed file.
// Each Write call emits [stream_id (1 byte)][length (4 bytes big-endian)][data].
// The mutex is shared between the stdout and stderr writers so that frames from
// concurrent goroutines are never interleaved mid-frame.
type muxWriter struct {
	mu     *sync.Mutex
	w      io.Writer
	stream byte // 1 = stdout, 2 = stderr
}

func (m *muxWriter) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var header [5]byte
	header[0] = m.stream
	binary.BigEndian.PutUint32(header[1:], uint32(len(p)))
	if _, err := m.w.Write(header[:]); err != nil {
		return 0, err
	}
	return m.w.Write(p)
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
	// Prefer the mux file when available: it preserves the original stdout/stderr
	// interleaving order captured during RunAndCache. Old caches without a mux
	// file fall back to the sequential (all-stdout-then-all-stderr) path below.
	if cc.MuxFilepath != "" {
		if muxFile, err := os.Open(cc.MuxFilepath); err == nil {
			defer muxFile.Close()
			return exitStatus, replayMux(muxFile)
		}
	}
	if _, err := io.Copy(os.Stdout, outFile); err != nil {
		return 0, err
	}
	if _, err := io.Copy(os.Stderr, errFile); err != nil {
		return 0, err
	}
	return exitStatus, nil
}

// replayMux reads a mux stream written by muxWriter and dispatches each frame
// to os.Stdout (stream_id=1) or os.Stderr (stream_id=2).
func replayMux(r io.Reader) error {
	var header [5]byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("reading mux header: %w", err)
		}
		stream := header[0]
		length := binary.BigEndian.Uint32(header[1:])
		data := make([]byte, length)
		if _, err := io.ReadFull(r, data); err != nil {
			return fmt.Errorf("reading mux data: %w", err)
		}
		switch stream {
		case 1:
			if _, err := os.Stdout.Write(data); err != nil {
				return err
			}
		case 2:
			if _, err := os.Stderr.Write(data); err != nil {
				return err
			}
		}
	}
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

	// Create a mux file to record stdout/stderr in original write order so that
	// ReplayByCache can reproduce the interleaving instead of replaying all
	// stdout before all stderr.
	var muxMu sync.Mutex
	var muxFile *cacheTempFile
	outWriters := []io.Writer{outFile.file, os.Stdout}
	errWriters := []io.Writer{errFile.file, os.Stderr}
	if cc.MuxFilepath != "" {
		muxFile, err = newCacheTempFile(cc.MuxFilepath)
		if err != nil {
			return 1, err
		}
		defer muxFile.Remove()
		outWriters = append(outWriters, &muxWriter{mu: &muxMu, w: muxFile.file, stream: 1})
		errWriters = append(errWriters, &muxWriter{mu: &muxMu, w: muxFile.file, stream: 2})
	}
	outWriter := io.MultiWriter(outWriters...)
	errWriter := io.MultiWriter(errWriters...)

	commands := cc.Command
	cmd := exec.Command(commands[0], commands[1:]...)
	// Pass the full process environment so commands can resolve binaries via PATH
	// and read user config via HOME. Only --env variables are in the cache key;
	// callers must list any env var that affects output.
	cmd.Env = os.Environ()
	cmd.Stdout = outWriter
	cmd.Stderr = errWriter
	err = cmd.Run()
	var exitStatus int
	if err2, ok := err.(*exec.ExitError); ok {
		exitStatus = err2.ExitCode()
		if exitStatus == -1 {
			exitStatus = 1 // signal-terminated; no numeric exit code available
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
	if muxFile != nil {
		if err := muxFile.Rename(); err != nil {
			return exitStatus, err
		}
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
		MuxFilepath:    filepath.Join(cacheDirectory, cacheKey+"_mux"),
	}

	exitStatus, err := commandCache.GetOrRun()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	exit(exitStatus)
}
