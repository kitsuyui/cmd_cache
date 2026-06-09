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

By default, `cmd_cache` keeps the newest 1024 complete cache entries in the
cache directory and prunes older entries after each run. Set
`--max-cache-entries=0` to disable pruning.

## Cache compatibility

New cache status files include a `cmd_cache status v1` header before the exit
status. Existing cache entries that contain only a numeric exit status are still
accepted for backward compatibility.

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

## Time-dependent commands

`cmd_cache` does not expire entries by time. There is no TTL, `--max-age`, or
date-based cache invalidation option. A cache hit is replayed until the command,
file dependencies, selected environment variables, or `--text` inputs produce a
different cache key, or until cache pruning removes the entry.

If the wrapped command changes output over time, include the intended time
window in the cache key explicitly:

```sh
cmd_cache --text "$(date -u +%Y-%m-%d)" -- curl https://example.com/daily.json
```

Prefer UTC or another explicitly chosen timezone for date keys. Plain
`date +%Y-%m-%d` uses the local timezone, so machines in different timezones can
create different daily cache entries while sharing the same cache directory.
If the command itself depends on local time or `TZ`, include that environment
variable with `--env TZ` or encode the chosen timezone in `--text`.

## Usage

### Example

```
$ cmd_cache --file depedingfile.go --env GOPATH -- go build
```

### Options

```
cmd_cache

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
```

# Build

```console
$ go get -d ./...
$ go build
```

## Development

Install [lefthook](https://github.com/evilmartians/lefthook) and register the hooks once:

```console
$ lefthook install
```

After that, on every commit the following check runs automatically:

- **pre-commit**: `shellcheck bin/*.sh`, `go vet ./...`

On every push the full local suite runs:

- **pre-push**: `shellcheck bin/*.sh`, `go vet ./...`, `go test ./...`

These hooks bring CI feedback earlier. The CI workflow still runs the full suite (including `-race -cover`) on every PR and push to main.

## LICENSE

### Source

The 3-Clause BSD License. See also LICENSE file.

### statically linked libraries:

- [golang/go](https://github.com/golang/go/) ... [BSD 3-Clause "New" or "Revised" License](https://github.com/golang/go/blob/master/LICENSE)
- [docopt/docopt-go](https://github.com/docopt/docopt.go) ... [MIT License](https://github.com/docopt/docopt.go/blob/master/LICENSE)
