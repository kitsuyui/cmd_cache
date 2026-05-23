# cmd_cache

![Coverage](https://raw.githubusercontent.com/kitsuyui/octocov-central/main/badges/kitsuyui/cmd_cache/coverage.svg)
[![Github All Releases](https://img.shields.io/github/downloads/kitsuyui/cmd_cache/total.svg)](https://github.com/kitsuyui/cmd_cache/releases/latest)


Run command with caching.

When cache exists, replay by cache.
When cache doesn't exist, run command and cache it.

Cache key can be generated with these way:

- Command
- File content (relative path and content)
- Environment variable (name and value)
- Text

Repeated `--file`, `--env`, and `--text` inputs are treated as dependency
sets, so their order does not affect the cache key.

`--file` values must be relative paths inside the current working directory.
Absolute paths and paths that escape the current working directory are rejected.

## Environment variable inheritance

The cache key only includes the environment variables listed with `--env`.
However, the wrapped command inherits the **entire** process environment when
it runs — not just the `--env` variables.

This means variables like `PATH`, `HOME`, `LANG`, and `TZ` silently affect
which binary is resolved and how the command behaves, even though they are
not part of the cache key.

If any unlisted environment variable affects the command's output, add it
with `--env` to include it in the cache key:

```sh
cmd_cache --env PATH --env LANG -- my-command
```

Shared cache directories across machines or CI environments with different
`PATH` or locale settings can produce stale replays from a cache hit.

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
 FILE      depending file under the current working directory. (e.g. prog.h)
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
