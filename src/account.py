"""Read account quota through the stock Codex app-server protocol."""
import asyncio
from contextlib import suppress
import json
import os
import time


def normalize(result):
    from events import _window
    buckets = result.get('rateLimitsByLimitId')
    limits = buckets.get('codex') if isinstance(buckets, dict) else None
    if not isinstance(limits, dict):
        limits = result.get('rateLimits')
    if not isinstance(limits, dict):
        return {}
    out = {'quota_scope': 'codex', 'quota_source': 'account', 'five_hour': None, 'weekly': None}
    for slot in ('primary', 'secondary'):
        value = limits.get(slot)
        if not isinstance(value, dict):
            continue
        field = {300: 'five_hour', 10080: 'weekly'}.get(value.get('windowDurationMins'))
        if field:
            out[field] = _window({'used_percent': value.get('usedPercent'), 'resets_at': value.get('resetsAt')})
    if any(out.get(k) for k in ('five_hour', 'weekly')):
        out['quota_updated_at'] = time.time()
    return out


async def fetch(home, executable):
    """One bounded helper, no credential parsing or model request."""
    process = None
    try:
        process = await asyncio.create_subprocess_exec(executable, 'app-server',
            env=dict(os.environ, CODEX_HOME=home), stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL,
            limit=1048576)
        async def send(value):
            process.stdin.write((json.dumps(value)+'\n').encode())
            await process.stdin.drain()
        async def response(ident):
            while True:
                raw = await process.stdout.readline()
                if not raw:
                    raise ValueError('account helper exited')
                value = json.loads(raw)
                if value.get('id') == ident:
                    if 'error' in value:
                        raise ValueError('account read failed')
                    return value.get('result', {})
        async with asyncio.timeout(15):
            await send({'id': 1, 'method': 'initialize', 'params': {'clientInfo': {'name': 'codex_status', 'version': '0.1.0'}}})
            await response(1)
            await send({'method': 'initialized'})
            await send({'id': 2, 'method': 'account/rateLimits/read'})
            return normalize(await response(2))
    except (OSError, ValueError, TimeoutError):
        return {'quota_error': 'Account quota unavailable'}
    finally:
        if process is not None and process.returncode is None:
            with suppress(ProcessLookupError):
                process.terminate()
            try:
                await asyncio.wait_for(process.wait(), 2)
            except TimeoutError:
                with suppress(ProcessLookupError):
                    process.kill()
                await process.wait()


class AccountCache:
    def __init__(self):
        self.entries = {}
        self.queries = 0

    def snapshot(self, home, executable, epoch):
        key = (home, executable)
        item = self.entries.get(key)
        if item is None or item['epoch'] != epoch:
            if item and item['task']:
                item['task'].cancel()
            item = self.entries[key] = {'epoch': epoch, 'task': None, 'data': {}, 'next': 0}
        task = item['task']
        if task and task.done():
            item['data'] = task.result()
            item['task'] = None
        if item['task'] is None and time.monotonic() >= item['next']:
            self.queries += 1
            item['next'] = time.monotonic() + 180
            item['task'] = asyncio.create_task(fetch(home, executable))
        return dict(item['data'])

    async def close(self):
        tasks = [x['task'] for x in self.entries.values() if x['task']]
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
