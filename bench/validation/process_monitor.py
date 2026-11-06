"""Owned-child process monitor for isolated gateway acceptance runs.

The monitor observes process exit, not AI readiness. A separate client must
verify the deployed candidate, protocol behavior, accounting and recovery time.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import time


class WorkingDirectoryLock:
    def __init__(self, path):
        self.stream = path.open('a+b')
        self.stream.seek(0, os.SEEK_END)
        if self.stream.tell() == 0:
            self.stream.write(b'0')
            self.stream.flush()
        self.stream.seek(0)
        try:
            if os.name == 'nt':
                import msvcrt
                msvcrt.locking(self.stream.fileno(), msvcrt.LK_NBLCK, 1)
            else:
                import fcntl
                fcntl.flock(self.stream.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            self.stream.close()
            raise RuntimeError('working directory is already monitored') from None

    def close(self):
        self.stream.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--executable', type=Path, required=True)
    parser.add_argument('--workdir', type=Path, required=True)
    parser.add_argument('--environment', type=Path, required=True)
    parser.add_argument('--events', type=Path, required=True)
    parser.add_argument('--expected-sha256')
    parser.add_argument('--interval', type=float, default=1.0)
    parser.add_argument('--stop-timeout', type=float, default=120.0)
    parser.add_argument('arguments', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if not 0.05 <= args.interval <= 60 or not 0 <= args.stop_timeout <= 3600:
        parser.error('interval or stop timeout is outside its allowed range')
    args.events.parent.mkdir(parents=True, exist_ok=True)

    def record(event, **fields):
        row = {'event': event, 'time': time.time(), 'monotonic': time.monotonic(),
               'monitor_pid': os.getpid(), **fields}
        with args.events.open('a', encoding='utf-8') as stream:
            stream.write(json.dumps(row, separators=(',', ':')) + '\n')

    lock = None
    process = None
    stopped = False

    def request_stop(signum, frame):
        nonlocal stopped
        stopped = True

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)
    try:
        if not args.workdir.is_absolute() or not args.workdir.is_dir():
            raise ValueError('working directory must be an existing absolute path')
        if not args.executable.is_absolute() or not args.executable.is_file():
            raise ValueError('executable must be an existing absolute path')
        lock = WorkingDirectoryLock(args.workdir / '.acceptance-monitor.lock')
        supplied = json.loads(args.environment.read_text(encoding='utf-8'))
        if not isinstance(supplied, dict) or any(
            not isinstance(k, str) or not isinstance(v, str)
            for k, v in supplied.items()
        ):
            raise ValueError('environment must contain string keys and values')
        for required in ['SQL_DSN', 'LOG_SQL_DSN', 'REDIS_CONN_STRING']:
            if not supplied.get(required, '').strip():
                raise ValueError('missing required infrastructure configuration')
        environment = os.environ.copy()
        environment.update(supplied)
        arguments = args.arguments[1:] if args.arguments[:1] == ['--'] else args.arguments
        with args.executable.open('rb') as stream:
            candidate_hash = hashlib.file_digest(stream, 'sha256').hexdigest()
        if args.expected_sha256 and candidate_hash != args.expected_sha256.lower():
            raise ValueError('candidate hash does not match')
        stop_file = args.workdir / '.monitor-stop'
        record('monitor_started', interval=args.interval, candidate_sha256=candidate_hash)
        while not stopped and not stop_file.exists():
            if process is not None and process.poll() is not None:
                record('child_exited', child_pid=process.pid, exit_code=process.returncode)
                process = None
            if process is None:
                try:
                    with args.executable.open('rb') as stream:
                        current_hash = hashlib.file_digest(stream, 'sha256').hexdigest()
                    if current_hash != candidate_hash:
                        record('candidate_changed')
                    else:
                        process = subprocess.Popen(
                            [str(args.executable), *arguments], cwd=args.workdir,
                            env=environment,
                            creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0,
                        )
                        record('child_started', child_pid=process.pid,
                               candidate_sha256=current_hash)
                except (OSError, ValueError) as error:
                    record('child_start_failed', error_type=type(error).__name__)
            time.sleep(args.interval)
        if process is not None and process.poll() is None:
            record('child_stop_requested', child_pid=process.pid)
            process.terminate()
            try:
                process.wait(timeout=args.stop_timeout)
            except subprocess.TimeoutExpired:
                record('child_forced_stop', child_pid=process.pid)
                process.kill()
                process.wait(timeout=10)
        record('monitor_stopped', child_pid=process.pid if process is not None else None)
        if stop_file.exists():
            stop_file.unlink()
        return 0
    except (OSError, ValueError, RuntimeError) as error:
        record('monitor_failed', error_type=type(error).__name__)
        return 2
    finally:
        if lock is not None:
            lock.close()


if __name__ == '__main__':
    raise SystemExit(main())
