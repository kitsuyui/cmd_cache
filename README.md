# cmd_cache

![Coverage](https://raw.githubusercontent.com/kitsuyui/octocov-central/main/badges/kitsuyui/cmd_cache/coverage.svg)
[![Github All Releases](https://img.shields.io/github/downloads/kitsuyui/cmd_cache/total.svg)](https://github.com/kitsuyui/cmd_cache/releases/latest)


Run command with caching.

When cache exists, replay by cache.
When cache doesn't exist, run command and cache it.

Cache key can be generated with these way:

- Command
- File content (path and content)
- Environment variable (name and value)
- Text

## Usage

### Example

```
$ cmd_cache --file depedingfile.go --env GOPATH -- go build
```

### Options

```
cmd_cache

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
```

# Build

```console
$ go get -d ./...
$ go build
```

## LICENSE

### Source

The 3-Clause BSD License. See also LICENSE file.

### statically linked libraries:

- [golang/go](https://github.com/golang/go/) ... [BSD 3-Clause "New" or "Revised" License](https://github.com/golang/go/blob/master/LICENSE)
- [docopt/docopt-go](https://github.com/docopt/docopt.go) ... [MIT License](https://github.com/docopt/docopt.go/blob/master/LICENSE)
