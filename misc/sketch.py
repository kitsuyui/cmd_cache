'''
cmd_cache

Usage:
cmd_cache [--cache-directory=DIRECTORY] [--algorithm=ALGORITHM] [(--file FILE | --env ENV | --text TEXT)...] -- [COMMAND...]
cmd_cache (--help | --version)

Arguments:
FILE      depending file. (e.g. prog.h)
ENV       depending environment variable. (e.g. LD_LIBRARY_PATH)
TEXT      text affecting command.
COMMAND   real command.

Options:
-h --help               	   Show this screen.
-V --version            	   Show version.
-v --verbose            	   Verbose mode.
--cache-directory=DIRECTORY    Cache directory [default: .cmd_cache]
--algorithm=ALGORITHM          hash algorithm for cache key [default: sha1]
'''

import asyncio
from dataclasses import dataclass
import fcntl
import hashlib
import os
import pathlib
import sys
import typing

from docopt import docopt


DEFAULT_ENCODING = sys.getdefaultencoding()


@dataclass(frozen=True)
class CommandContext:
    command: typing.Sequence[str]
    texts: typing.Sequence[str]
    envnames: typing.Sequence[str]
    filenames: typing.Sequence[str]

    def file_contents(self):
        for filename in self.filenames:
            with open(filename, 'rb') as f:
                fcntl.flock(f, fcntl.LOCK_EX)
                yield filename.encode(DEFAULT_ENCODING)
                for block in f:
                    yield block

    def env_contents(self):
        for envname in self.envnames:
            if envname in os.environ:
                yield envname.encode(DEFAULT_ENCODING)
                yield os.environ[envname].encode(DEFAULT_ENCODING)

    def write_to_hash(self, hash_object):
        for command_part in self.command:
            hash_object.update(command_part.encode(DEFAULT_ENCODING))
        for text in self.texts:
            hash_object.update(text.encode(DEFAULT_ENCODING))
        for content in self.file_contents():
            hash_object.update(content)
        for content in self.env_contents():
            hash_object.update(content)
        return hash_object.hexdigest()


class CacheNotFound(Exception):
    pass


@dataclass(frozen=True)
class CommandCache:
    command: typing.Sequence[str]
    status_filepath: pathlib.Path
    out_filepath: pathlib.Path
    err_filepath: pathlib.Path

    async def replay_by_cache(self):
        try:
            with self.out_filepath.open('rb') as outf, \
                self.err_filepath.open('rb') as errf, \
                self.status_filepath.open('rb') as statusf \
            :
                fcntl.flock(outf, fcntl.LOCK_EX)
                fcntl.flock(errf, fcntl.LOCK_EX)
                fcntl.flock(statusf, fcntl.LOCK_EX)
                await asyncio.gather(tee(outf, sys.stdout.buffer),
                                     tee(errf, sys.stderr.buffer))
                return int(statusf.read())
        except FileNotFoundError:
            raise CacheNotFound

    async def run_and_cache(self):
        process = await asyncio.create_subprocess_exec(*self.command,
                                                       stdout=asyncio.subprocess.PIPE,
                                                       stderr=asyncio.subprocess.PIPE,
                                                       universal_newlines=False)
        loop = asyncio.get_event_loop()
        with self.out_filepath.open('wb') as outf, \
            self.err_filepath.open('wb') as errf, \
            self.status_filepath.open('wb') as statusf \
        :
            fcntl.flock(outf, fcntl.LOCK_EX)
            fcntl.flock(errf, fcntl.LOCK_EX)
            fcntl.flock(statusf, fcntl.LOCK_EX)
            await asyncio.gather(tee(process.stdout, outf, sys.stdout.buffer),
                                 tee(process.stderr, errf, sys.stderr.buffer),
                                 process.wait())
            statusf.write(f'{process.returncode}'.encode(DEFAULT_ENCODING))
        return process.returncode


async def tee(input, *outfiles, chunk_size=1024):
    while True:
        if asyncio.iscoroutinefunction(input.read):
            line = await input.read(chunk_size)
        else:
            line = input.read(chunk_size)
        if not line:
            break
        for out in outfiles:
            out.write(line)


async def main():
    arguments = docopt(__doc__, version='0.0.1')
    cache_directory = pathlib.Path(arguments['--cache-directory'])
    cache_directory.mkdir(parents=True, exist_ok=True)
    command = arguments['COMMAND']
    algorithm = arguments['--algorithm']
    hash_object = hashlib.new(algorithm)
    command_context = CommandContext(command=command,
                                     texts=arguments['TEXT'],
                                     envnames=arguments['ENV'],
                                     filenames=arguments['FILE'])

    command_context.write_to_hash(hash_object)
    cache_key = hash_object.hexdigest()

    status_file = cache_directory / f'{cache_key}'
    outfile = cache_directory / f'{cache_key}_out'
    errfile = cache_directory / f'{cache_key}_err'

    command_cache = CommandCache(command=command,
                                 status_filepath=status_file,
                                 err_filepath=errfile,
                                 out_filepath=outfile)

    try:
        return_code = await command_cache.replay_by_cache()
    except CacheNotFound:
        return_code = await command_cache.run_and_cache()
    sys.exit(return_code)


if __name__ == '__main__':
    asyncio.run(main())
