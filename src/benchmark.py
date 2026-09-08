"""Owned synthetic sessions in real tmux; no Codex/API execution or auth reads."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import uuid
from binding import process_identity, auth_epoch, _process_executable
from collector import validate_runtime, write_run_metadata

ENTRY = Path(__file__).resolve().with_name('codex-status.py')


def status(runtime):
    with socket.socket(socket.AF_UNIX) as sock:
        sock.settimeout(3)
        sock.connect(str(runtime/'collector.sock'))
        sock.sendall(b'{"v":1,"op":"status"}\n')
        with sock.makefile('rb') as stream:
            return json.loads(stream.readline())


def times(text):
    parts = text.replace('-', ':').split(':')
    total = 0
    for part in parts:
        total = total * 60 + float(part)
    return total


def process_stats(roots):
    result = subprocess.run(['ps','-axo','pid=,ppid=,rss=,time='],capture_output=True,text=True,check=True)
    rows = {}
    for line in result.stdout.splitlines():
        fields = line.split()
        if len(fields) == 4:
            rows[int(fields[0])] = (int(fields[1]), int(fields[2]), times(fields[3]))
    selected = set(roots)
    for _ in range(10):
        extra = {pid for pid, row in rows.items() if row[0] in selected}
        if extra <= selected:
            break
        selected |= extra
    return {'cpu_seconds':sum(rows[p][2] for p in selected if p in rows),
            'rss_kib':sum(rows[p][1] for p in selected if p in rows),
            'pids':sorted(p for p in selected if p in rows)}


def measure(roots, duration, runtime=None):
    def sample():
        current = process_stats(roots)
        if runtime:
            current['collector'] = status(runtime)
            current['cpu_seconds'] += current['collector']['children_cpu_seconds']
        return current
    start = sample(); began = time.monotonic()
    time.sleep(duration)
    end = sample(); elapsed = time.monotonic()-began
    return {'elapsed_seconds':elapsed, 'one_core_cpu_percent':max(0,end['cpu_seconds']-start['cpu_seconds'])/elapsed*100,
            'rss_kib':end['rss_kib'], 'process_count':len(end['pids']), 'start':start, 'end':end}


def benchmark(count, duration):
    root = Path(tempfile.mkdtemp(prefix='hud-bench-', dir='/tmp'))
    runtime = validate_runtime(root/'runtime')
    tmux_socket = root/'tmux.sock'
    base = ['tmux','-S',str(tmux_socket)]
    handles, panes = [], []
    control = None
    collector_pid = None
    def tmux(*args):
        return subprocess.run(base+list(args),capture_output=True,text=True,check=True,timeout=5).stdout.strip()
    result = {'kind':'synthetic-root-rollouts-real-tmux-renderers', 'sessions':count,
              'scope':'CPU includes collector reaped subprocesses and live renderer/tmux processes. No model calls.',
              'runtime':str(runtime),
              'source_sha256':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in Path(__file__).parent.glob('*.py')},
              'native_client_sha256':hashlib.sha256((Path(__file__).parent.parent/'build/codex-status-client').read_bytes()).hexdigest() if (Path(__file__).parent.parent/'build/codex-status-client').exists() else None}
    try:
        repo = root/'repo'; repo.mkdir(); (repo/'.git').mkdir(); (repo/'.git/HEAD').write_text('ref: refs/heads/benchmark\n')
        sleeper = f'{sys.executable} -c "import time; time.sleep(3600)"'
        main = tmux('-f','/dev/null','new-session','-d','-s','benchmark','-x','160','-y',str(max(80,count*6+20)),'-P','-F','#{pane_id}',sleeper)
        tmux('set-option','-w','window-size','manual')
        pid = int(tmux('display-message','-p','#{pid}'))
        runs = []
        for i in range(count):
            pane = tmux('split-window','-d','-v','-l','4','-t',main,'-P','-F','#{pane_id}',sleeper)
            panes.append(pane)
            home = root/f'home-{i}'; folder = home/'sessions'; folder.mkdir(parents=True)
            session = str(uuid.uuid4()); run = str(uuid.uuid4())
            path = folder/f'rollout-fixture-{session}.jsonl'
            observed = __import__('datetime').datetime.now(__import__('datetime').UTC).isoformat()
            records = [
                {'timestamp':observed,'type':'session_meta','payload':{'id':session,'session_id':session,'parent_thread_id':None,'source':'cli','cwd':str(repo),'cli_version':'0.153.4'}},
                {'timestamp':observed,'type':'turn_context','payload':{'turn_id':'fixture','model':f'fixture-{i}','effort':'medium'}},
                {'timestamp':observed,'type':'event_msg','payload':{'type':'task_started','turn_id':'fixture'}},
                {'timestamp':observed,'type':'event_msg','payload':{'type':'token_count','info':{'model_context_window':100000,'last_token_usage':{'total_tokens':34000}},'rate_limits':{'limit_id':'codex','primary':{'window_minutes':300,'used_percent':i,'resets_at':int(time.time())+3600},'secondary':{'window_minutes':10080,'used_percent':50,'resets_at':int(time.time())+86400}}}}
            ]
            path.write_text(''.join(json.dumps(row)+'\n' for row in records))
            handles.append(path.open('rb'))
            meta = {'v':1,'id':run,'pid':os.getpid(),'started':process_identity(os.getpid()),'executable':str(_process_executable(os.getpid())),
                    'home':str(home),'cwd':str(repo),'tmux_socket':str(tmux_socket),'codex_pane':main,'hud_pane':pane,'expected_session':session,
                    'launched_at':time.time(),'auth_epoch':auth_epoch(str(home))}
            write_run_metadata(runtime,meta)
            runs.append(run)
        control = subprocess.Popen(base+['-C','attach-session','-t','benchmark'],stdin=subprocess.PIPE,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        tmux('resize-window','-x','160','-y',str(max(80,count*6+20)))
        result['baseline'] = [measure([pid],duration) for _ in range(3)]
        import shlex
        for pane, run in zip(panes,runs):
            tmux('respawn-pane','-k','-t',pane,shlex.join([sys.executable,str(ENTRY),'__render',str(runtime),run]))
        deadline = time.monotonic()+30
        snapshot = None
        while time.monotonic()<deadline:
            try:
                snapshot = status(runtime)
                if snapshot['clients'] == count and snapshot['metrics']['binding_checks'] >= count and snapshot['metrics']['bytes_read'] > 0:
                    break
            except (OSError, ValueError):
                pass
            time.sleep(0.2)
        if not snapshot or snapshot['clients'] != count:
            raise RuntimeError('renderers did not connect')
        collector_pid = snapshot['pid']
        time.sleep(1)
        result['active'] = [measure([pid,collector_pid],duration,runtime) for _ in range(3)]
        result['sample_screen'] = tmux('capture-pane','-p','-t',panes[0])
        result['last_screen'] = tmux('capture-pane','-p','-t',panes[-1])
        control.terminate(); control.wait(timeout=3); control.stdin.close(); control=None
        time.sleep(6)
        before = status(runtime)
        result['hidden'] = measure([pid,collector_pid],duration,runtime)
        after = status(runtime)
        result['hidden_data_stopped'] = all(before['metrics'][key] == after['metrics'][key] for key in ('bytes_read','binding_checks','session_reads')) and before['head_reads'] == after['head_reads']
        for pane in panes:
            tmux('kill-pane','-t',pane)
        deadline = time.monotonic()+6
        while (runtime/'collector.sock').exists() and time.monotonic()<deadline:
            time.sleep(0.1)
        result['collector_socket_removed'] = not (runtime/'collector.sock').exists()
        result['collector_alive_after_close'] = process_identity(collector_pid) is not None
        result['git_queries'] = after['metrics']['git_queries']
        result['head_reads'] = after['head_reads']
        result['passed'] = result['hidden_data_stopped'] and result['collector_socket_removed'] and not result['collector_alive_after_close'] and result['git_queries'] == 0 and result['head_reads'] == 1
        return result
    finally:
        if control:
            control.terminate(); control.wait(timeout=3); control.stdin.close()
        subprocess.run(base+['kill-server'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        for handle in handles:
            handle.close()
        # Keep only an explicit failed runtime location, never conversation/auth data.
        import shutil
        shutil.rmtree(root)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sessions',type=int,choices=(1,36),default=36)
    parser.add_argument('--interval',type=float,default=5)
    parser.add_argument('--output',type=Path,required=True)
    args = parser.parse_args()
    result = benchmark(args.sessions,args.interval)
    args.output.write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps({k:result[k] for k in ('sessions','passed','hidden_data_stopped','git_queries','head_reads','collector_alive_after_close')}))
    sys.exit(0 if result['passed'] else 1)
