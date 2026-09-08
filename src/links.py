"""Construct terminal link targets without launching a browser."""
import asyncio
from contextlib import suppress
from pathlib import Path
import re
import subprocess
import time
import unicodedata
from urllib.parse import quote, urlsplit, urlunsplit

USAGE_URL = 'https://chatgpt.com/codex/settings/usage'


def repository_url(remote):
    if not isinstance(remote, str) or len(remote) > 4096 or any(unicodedata.category(c).startswith('C') for c in remote):
        return None
    remote = remote.strip()
    if '://' not in remote:
        match = re.fullmatch(r'(?:[^/@:]+@)?([A-Za-z0-9.-]+):(.+)', remote)
        if not match:
            return None
        remote = 'ssh://' + match[1] + '/' + match[2]
    try:
        parsed = urlsplit(remote)
        host = parsed.hostname
        if parsed.scheme not in ('https', 'http', 'ssh') or not host or not re.fullmatch(r'[A-Za-z0-9.-]+', host):
            return None
        # Never propagate credentials, query tokens, or fragments into terminal links.
        authority = host
        if parsed.scheme in ('http', 'https') and parsed.port:
            authority += ':' + str(parsed.port)
        path = parsed.path.rstrip('/')
        if path.endswith('.git'):
            path = path[:-4]
        if not path or path == '/':
            return None
        return urlunsplit(('https', authority, quote(path, safe='/%'), '', ''))
    except ValueError:
        return None


def branch_url(repository, branch):
    if not repository or not isinstance(branch, str) or not branch or any(unicodedata.category(c).startswith('C') for c in branch):
        return None
    host = urlsplit(repository).hostname or ''
    route = '/-/tree/' if 'gitlab' in host else '/src/' if host == 'bitbucket.org' else '/tree/'
    if host == 'codeberg.org':
        route = '/src/branch/'
    return repository + route + quote(branch, safe='')


def read_remote(cwd):
    try:
        result = subprocess.run(['git', '-C', cwd, 'config', '--get', 'remote.origin.url'],
            capture_output=True, text=True, timeout=1, check=False)
        return repository_url(result.stdout.rstrip('\n')) if result.returncode == 0 else None
    except (OSError, subprocess.SubprocessError):
        return None


class LinkCache:
    def __init__(self):
        self.entries = {}
        self.queries = 0

    def snapshot(self, cwd, branch, session=None):
        item = self.entries.setdefault(cwd, {'url': None, 'next': 0, 'task': None})
        if item['task'] and item['task'].done():
            with suppress(asyncio.CancelledError):
                item['url'] = item['task'].result()
            item['task'] = None
        if item['task'] is None and time.monotonic() >= item['next']:
            self.queries += 1
            item['next'] = time.monotonic() + 60
            item['task'] = asyncio.create_task(asyncio.to_thread(read_remote, cwd))
        return {'directory_url': Path(cwd).absolute().as_uri(), 'usage_url': USAGE_URL,
                'branch_url': branch_url(item['url'], branch),
                'session_url': Path(session).absolute().as_uri() if session else None}

    async def close(self):
        tasks = [item['task'] for item in self.entries.values() if item['task']]
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
