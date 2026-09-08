import asyncio
import unittest
from unittest.mock import patch
from links import LinkCache, repository_url, branch_url


class LinkTests(unittest.TestCase):
    def test_ssh_and_https_remote(self):
        for remote in ('git@github.com:owner/repo.git', 'https://github.com/owner/repo.git', 'ssh://git@github.com/owner/repo.git'):
            self.assertEqual(repository_url(remote), 'https://github.com/owner/repo')

    def test_credentials_query_and_fragment_never_reach_output(self):
        self.assertEqual(repository_url('https://user:secret@github.com/owner/repo.git?token=secret#secret'), 'https://github.com/owner/repo')

    def test_control_characters_and_non_web_remotes_rejected(self):
        for remote in ('file:///repo', '/local/repo', 'https://github.com/repo\x1b]8;;bad', 'https://github.com/repo\n'):
            self.assertIsNone(repository_url(remote))

    def test_branch_with_slash_and_reserved_characters(self):
        self.assertEqual(branch_url('https://github.com/o/r', 'feat/a#b'), 'https://github.com/o/r/tree/feat%2Fa%23b')
        self.assertEqual(branch_url('https://gitlab.com/o/r', 'main'), 'https://gitlab.com/o/r/-/tree/main')


class CacheTests(unittest.IsolatedAsyncioTestCase):
    async def test_shared_lookup_and_escaped_file_link(self):
        cache = LinkCache()
        with patch('links.read_remote', return_value='https://github.com/o/r') as read:
            first = cache.snapshot('/tmp/a b#c', 'main')
            self.assertEqual(first['directory_url'], 'file:///tmp/a%20b%23c')
            await cache.entries['/tmp/a b#c']['task']
            second = cache.snapshot('/tmp/a b#c', 'main', '/tmp/session.jsonl')
            self.assertEqual(second['branch_url'], 'https://github.com/o/r/tree/main')
            cache.snapshot('/tmp/a b#c', 'main')
            self.assertEqual(read.call_count, 1)
            await cache.close()
