package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"log"
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

func (cc CommandContext) writeToHash(h hash.Hash) {
	for _, cmd := range cc.Command {
		io.WriteString(h, cmd)
	}
	for _, text := range cc.Texts {
		io.WriteString(h, text)
	}
	for _, filename := range cc.Filenames {
		io.WriteString(h, filename)
		f, err := os.Open(filename)
		if err != nil {
			log.Fatal(err)
		}
		io.Copy(h, f)
		defer f.Close()
	}
	for _, envname := range cc.EnvironmentVariableNames {
		io.WriteString(h, envname)
		if value, ok := os.LookupEnv(envname); ok {
			io.WriteString(h, value)
		}
	}
}

type CommandCache struct {
	Command        []string
	StatusFilepath string
	OutFilepath    string
	ErrFilepath    string
}

func (cc CommandCache) replayByCache() (int, error) {
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
	exitStatusText, err := ioutil.ReadAll(statusFile)
	if err != nil {
		return 0, err
	}
	exitStatus, err := strconv.Atoi(string(exitStatusText))
	io.Copy(os.Stdout, outFile)
	io.Copy(os.Stderr, errFile)
	return exitStatus, nil
}

func (cc CommandCache) runAndCache() int {
	outFile, err := os.OpenFile(cc.OutFilepath, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		log.Fatal(err)
	}
	defer outFile.Close()
	errFile, err := os.OpenFile(cc.ErrFilepath, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		log.Fatal(err)
	}
	defer errFile.Close()
	statusFile, err := os.OpenFile(cc.StatusFilepath, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		log.Fatal(err)
	}
	defer statusFile.Close()
	outWriter := io.MultiWriter(outFile, os.Stdout)
	errWriter := io.MultiWriter(errFile, os.Stderr)

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
	}
	statusFile.Write([]byte(strconv.Itoa(exitStatus)))
	return exitStatus
}

var version string
var exit = os.Exit

func main() {
	opts, err := docopt.ParseDoc(COMMAND_USAGE)
	if err != nil {
		fmt.Println(err)
		exit(1)
	}
	if showVersion, _ := opts.Bool("--version"); showVersion {
		println(version)
		return
	}
	cacheDirectory := opts["--cache-directory"].(string)
	os.MkdirAll(cacheDirectory, 0755)
	if err != nil {
		fmt.Println(err)
		exit(1)
	}

	commands := opts["COMMAND"].([]string)
	commandContext := CommandContext{
		Command: commands,
		Texts:   opts["TEXT"].([]string),
		EnvironmentVariableNames: opts["ENV"].([]string),
		Filenames:                opts["FILE"].([]string),
	}

	h := sha1.New()
	commandContext.writeToHash(h)
	cacheKey := hex.EncodeToString(h.Sum(nil))

	commandCache := CommandCache{
		Command:        commands,
		StatusFilepath: filepath.Join(cacheDirectory, cacheKey),
		OutFilepath:    filepath.Join(cacheDirectory, cacheKey+"_out"),
		ErrFilepath:    filepath.Join(cacheDirectory, cacheKey+"_err"),
	}

	var exitStatus int
	exitStatus, err = commandCache.replayByCache()
	if err != nil {
		exitStatus = commandCache.runAndCache()
	}
	exit(exitStatus)
}
