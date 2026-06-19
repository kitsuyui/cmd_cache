package main

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
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
	"time"

	"github.com/docopt/docopt-go"
)

const COMMAND_USAGE = `cmd_cache
Usage:
 cmd_cache [--cache-directory=DIRECTORY] [--max-cache-entries=COUNT] [(--file FILE | --env ENV | --text TEXT)...] -- [COMMAND...]
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
 --max-cache-entries=COUNT      Maximum complete cache entries to keep; 0 disables pruning [default: 1024]
`

const cacheStatusHeader = "cmd_cache status v1\n"

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

type cacheEntry struct {
	Key            string
	StatusFilepath string
	OutFilepath    string
	ErrFilepath    string
	MuxFilepath    string
	LockFilepath   string
	ModTime        time.Time
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
	// Check the status file first: if it is absent or invalid, avoid opening
	// the output files unnecessarily (cache-miss is the common case).
	statusFile, err := os.Open(cc.StatusFilepath)
	if err != nil {
		return 0, err
	}
	defer statusFile.Close()
	exitStatusText, err := io.ReadAll(statusFile)
	if err != nil {
		return 0, err
	}
	exitStatus, err := parseCachedExitStatus(exitStatusText)
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
	// Buffer both output files before writing. If either read fails no bytes
	// are written to stdout or stderr, so a fallback to RunAndCache cannot
	// produce duplicate output.
	outBuf, err := os.ReadFile(cc.OutFilepath)
	if err != nil {
		return 0, err
	}
	errBuf, err := os.ReadFile(cc.ErrFilepath)
	if err != nil {
		return 0, err
	}
	if _, err := os.Stdout.Write(outBuf); err != nil {
		return 0, err
	}
	if _, err := os.Stderr.Write(errBuf); err != nil {
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

func parseCachedExitStatus(content []byte) (int, error) {
	text := string(content)
	if strings.HasPrefix(text, cacheStatusHeader) {
		return strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(text, cacheStatusHeader), "\n"))
	}
	return strconv.Atoi(text)
}

func formatCachedExitStatus(exitStatus int) []byte {
	return []byte(cacheStatusHeader + strconv.Itoa(exitStatus) + "\n")
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
	if _, err := statusFile.file.Write(formatCachedExitStatus(exitStatus)); err != nil {
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

func parseMaxCacheEntries(value string) (int, error) {
	maxCacheEntries, err := strconv.Atoi(value)
	if err != nil || maxCacheEntries < 0 {
		return 0, fmt.Errorf("--max-cache-entries must be a non-negative integer: %q", value)
	}
	return maxCacheEntries, nil
}

func isCacheKey(name string) bool {
	if len(name) != sha1.Size*2 {
		return false
	}
	for _, r := range name {
		if !('0' <= r && r <= '9') && !('a' <= r && r <= 'f') {
			return false
		}
	}
	return true
}

func collectCompleteCacheEntries(cacheDirectory string) ([]cacheEntry, error) {
	dirEntries, err := os.ReadDir(cacheDirectory)
	if err != nil {
		return nil, err
	}

	entries := make([]cacheEntry, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		if dirEntry.IsDir() {
			continue
		}
		key := dirEntry.Name()
		if !isCacheKey(key) ||
			strings.HasPrefix(key, ".") ||
			strings.HasSuffix(key, "_out") ||
			strings.HasSuffix(key, "_err") ||
			strings.HasSuffix(key, "_mux") ||
			strings.HasSuffix(key, ".lock") {
			continue
		}

		statusPath := filepath.Join(cacheDirectory, key)
		outPath := filepath.Join(cacheDirectory, key+"_out")
		errPath := filepath.Join(cacheDirectory, key+"_err")
		statusInfo, err := dirEntry.Info()
		if err != nil {
			continue
		}
		outInfo, err := os.Stat(outPath)
		if err != nil {
			continue
		}
		errInfo, err := os.Stat(errPath)
		if err != nil {
			continue
		}

		modTime := statusInfo.ModTime()
		for _, info := range []os.FileInfo{outInfo, errInfo} {
			if info.ModTime().After(modTime) {
				modTime = info.ModTime()
			}
		}

		entries = append(entries, cacheEntry{
			Key:            key,
			StatusFilepath: statusPath,
			OutFilepath:    outPath,
			ErrFilepath:    errPath,
			MuxFilepath:    filepath.Join(cacheDirectory, key+"_mux"),
			LockFilepath:   statusPath + ".lock",
			ModTime:        modTime,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ModTime.Equal(entries[j].ModTime) {
			return entries[i].Key < entries[j].Key
		}
		return entries[i].ModTime.Before(entries[j].ModTime)
	})
	return entries, nil
}

func pruneCacheEntries(cacheDirectory string, maxEntries int) error {
	if maxEntries < 0 {
		return fmt.Errorf("maxEntries must be non-negative: %d", maxEntries)
	}

	entries, err := collectCompleteCacheEntries(cacheDirectory)
	if err != nil {
		return err
	}
	if len(entries) <= maxEntries {
		return nil
	}

	var errs []error
	for _, entry := range entries[:len(entries)-maxEntries] {
		for _, path := range []string{entry.StatusFilepath, entry.OutFilepath, entry.ErrFilepath, entry.MuxFilepath, entry.LockFilepath} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
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
	maxCacheEntries, err := parseMaxCacheEntries(opts["--max-cache-entries"].(string))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
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
	if maxCacheEntries > 0 {
		if pruneErr := pruneCacheEntries(cacheDirectory, maxCacheEntries); pruneErr != nil {
			fmt.Fprintln(os.Stderr, pruneErr)
		}
	}
	exit(exitStatus)
}
