import json
import os
from pathlib import Path
import tempfile
import unittest
from collector import validate_runtime, write_run_metadata, BranchCache


class CollectorContract(unittest.TestCase):
    def test_private_directory_rejects_symlink_and_world_readable(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / 'link').symlink_to(root, target_is_directory=True)
            with self.assertRaises(ValueError):
                validate_runtime(root / 'link')
            public = root / 'public'; public.mkdir(mode=0o755)
            with self.assertRaises(ValueError):
                validate_runtime(public)
            private = validate_runtime(root / 'private')
            self.assertEqual(private.stat().st_mode & 0o777, 0o700)

    def test_metadata_atomic_private_and_no_parent_escape(self):
        with tempfile.TemporaryDirectory() as td:
            root = validate_runtime(Path(td) / 'runtime')
            run = '12345678-1234-1234-1234-123456789abc'
            path = write_run_metadata(root, {'id': run, 'v': 1})
            self.assertEqual(json.loads(path.read_text()), {'id': run, 'v': 1})
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(ValueError):
                write_run_metadata(root, {'id': '../escape'})

    def test_one_head_read_for_same_repo_then_refresh_on_change(self):
        with tempfile.TemporaryDirectory() as td:
            repo = Path(td); (repo / '.git').mkdir()
            (repo / '.git/HEAD').write_text('ref: refs/heads/main\n')
            nested = repo / 'sub'; nested.mkdir()
            cache = BranchCache()
            self.assertEqual(cache.branch(repo), 'main')
            self.assertEqual(cache.branch(nested), 'main')
            self.assertEqual(cache.head_reads, 1)
            (repo / '.git/HEAD').write_text('ref: refs/heads/next\n')
            self.assertEqual(cache.branch(repo), 'next')
            self.assertEqual(cache.head_reads, 2)

    def test_worktree_head_is_not_common_repo_head(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td); repo = root/'worktree'; repo.mkdir()
            gd = root/'metadata'; gd.mkdir()
            (repo/'.git').write_text('gitdir: ../metadata\n')
            (gd/'HEAD').write_text('ref: refs/heads/worktree\n')
            self.assertEqual(BranchCache().branch(repo), 'worktree')


if __name__ == '__main__':
    unittest.main()


from unittest.mock import patch
import asyncio
from collector import Collector, Run
import types


class CollectorScheduling(unittest.IsolatedAsyncioTestCase):
    async def test_duplicate_subscribers_share_reads_hidden_reads_stop(self):
        class Writer:
            def __init__(self): self.messages = []
            def write(self, value): self.messages.append(json.loads(value))
            async def drain(self): pass
            def close(self): pass
        class Reader:
            invalidated = False
            bytes_read = 0
            def __init__(self, path, session): self.calls = 0
            def read(self):
                self.calls += 1
                return {'model': 'fixture-model', 'session_id': 'fixture', 'quota_updated_at': None}
            def reset_after_account_change(self): pass
        with tempfile.TemporaryDirectory() as td:
            meta = {'id':'r', 'home':td, 'cwd':td, 'tmux_socket':'/fixture.sock', 'hud_pane':'%1', 'launched_at':0}
            engine = Collector(Path(td))
            engine.runs['r'] = Run(meta, ('epoch',))
            writers = [Writer() for _ in range(36)]
            engine.clients = {w:{'run':'r', 'last':None} for w in writers}
            path = Path(td)/'rollout-12345678-1234-1234-1234-123456789abc.jsonl'
            binding = types.SimpleNamespace(auth_epoch=lambda _:('epoch',), bind_run=lambda _:path)
            events = types.SimpleNamespace(SessionReader=Reader)
            with patch.dict('sys.modules', {'binding':binding, 'events':events}), patch('collector.pane_visibility', return_value={'%1':True}):
                await engine.tick()
                self.assertEqual(engine.runs['r'].reader.calls, 1)
                self.assertEqual(engine.metrics['binding_checks'], 1)
                self.assertEqual(engine.metrics['tmux_queries'], 1)
                self.assertTrue(all(w.messages[-1]['model'] == 'fixture-model' for w in writers))
                engine.visible = {'/fixture.sock':{'%1':False}}
                await engine.tick()
                self.assertEqual(engine.runs['r'].reader.calls, 1)
                self.assertTrue(all(w.messages[-1]['state'] == 'hidden' for w in writers))

    async def test_account_change_clears_quota_before_new_data(self):
        class Reader:
            invalidated = False
            def __init__(self): self.reset = False
            def reset_after_account_change(self): self.reset = True
            def read(self):
                return {'quota_updated_at':None, 'five_hour':None} if self.reset else {'quota_updated_at':1, 'five_hour':{'remaining':88}}
        with tempfile.TemporaryDirectory() as td:
            engine = Collector(Path(td))
            meta = {'id':'r', 'home':td, 'cwd':td, 'tmux_socket':'/fixture.sock', 'hud_pane':'%1'}
            reader = Reader()
            engine.runs['r'] = Run(meta, ('old',), reader, Path(td)/'file', last_binding=__import__('time').monotonic())
            binding = types.SimpleNamespace(auth_epoch=lambda _:('new',), bind_run=lambda _:None)
            with patch.dict('sys.modules', {'binding':binding, 'events':types.SimpleNamespace(SessionReader=Reader)}), patch('collector.pane_visibility', return_value={'%1':True}):
                await engine.tick()
                self.assertTrue(reader.reset)
                self.assertIsNone(engine.runs['r'].snapshot['five_hour'])
                self.assertTrue(engine.runs['r'].account_changed)


class AnsiDelivery(unittest.IsolatedAsyncioTestCase):
    async def test_server_renders_terminal_frames_for_thin_client(self):
        class Writer:
            def __init__(self): self.frames = []
            def write(self, value): self.frames.append(value)
            async def drain(self): pass
            def close(self): pass
        with tempfile.TemporaryDirectory() as td:
            engine = Collector(Path(td)); writer = Writer()
            engine.clients[writer] = {'run':'fixture', 'last':None, 'format':'ansi', 'width':80}
            await engine.send(writer, {'v':1,'state':'pending','model':'model\x1b]52;c;secret\x07','cwd':'/fixture'})
            self.assertTrue(writer.frames[0].startswith(b'\x1b[?25l\x1b[H'))
            self.assertNotIn(b'secret', writer.frames[0])
            self.assertEqual(writer.frames[0].count(b'\r\n'), 2)
            await engine.send(writer, {'v':1,'state':'pending','model':'model\x1b]52;c;secret\x07','cwd':'/fixture'})
            self.assertEqual(len(writer.frames), 1)
