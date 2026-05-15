# misc

This directory contains a historical Python prototype (`sketch.py`) written before the Go implementation existed.

## sketch.py

`sketch.py` is a proof-of-concept implementation of `cmd_cache` in Python using `asyncio` and `fcntl.flock`. It predates the Go implementation (`main.go`) and is **not actively maintained**.

It includes features that were explored during the design phase but not carried forward into the Go version (e.g., `--algorithm` option, `--verbose` flag).

**Status**: reference-only; not intended to be run or maintained alongside the Go implementation.
If you are looking for the canonical implementation, see `main.go`.
