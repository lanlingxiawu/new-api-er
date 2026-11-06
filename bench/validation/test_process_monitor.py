import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest


SCRIPT = Path(__file__).with_name('process_monitor.py')


class ProcessMonitorTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='newapi-monitor-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.events = self.root / 'events.jsonl'
        self.env_file = self.root / 'environment.json'
        self.env_file.write_text(json.dumps({
            'SQL_DSN': 'synthetic-main-not-a-real-connection',
            'LOG_SQL_DSN': 'synthetic-log-not-a-real-connection',
            'REDIS_CONN_STRING': 'synthetic-redis-not-a-real-connection',
        }), encoding='utf-8')
        self.child = self.root / 'child.py'
        self.child.write_text('import time\ntime.sleep(60)\n', encoding='utf-8')

    def start(self, extra=None):
        command = [sys.executable, str(SCRIPT), '--executable', sys.executable,
                   '--workdir', str(self.root), '--environment', str(self.env_file),
                   '--events', str(self.events), '--interval', '1',
                   '--stop-timeout', '2']
        if extra:
            command.extend(extra)
        command.extend(['--', str(self.child)])
        output = (self.root / ('monitor-' + str(time.monotonic_ns()) + '.log')).open('wb')
        process = subprocess.Popen(command, stdout=output, stderr=subprocess.STDOUT)
        self.addCleanup(output.close)
        self.addCleanup(self.stop, process)
        return process

    def stop(self, process):
        if process.poll() is not None:
            return
        if os.name == 'nt':
            (self.root / '.monitor-stop').touch()
        else:
            process.send_signal(signal.SIGTERM)
        try:
            process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)

    def wait_event(self, kind, after=0):
        end = time.monotonic() + 10
        while time.monotonic() < end:
            if self.events.exists():
                for line in self.events.read_text(encoding='utf-8').splitlines():
                    try:
                        row = json.loads(line)
                    except ValueError:
                        continue
                    if row['event'] == kind and row['monotonic'] > after:
                        return row
            time.sleep(0.05)
        self.fail('missing monitor event: ' + kind)

    def test_terminated_child_restarts_and_monitor_stops_its_child(self):
        process = self.start()
        first = self.wait_event('child_started')
        os.kill(first['child_pid'], signal.SIGTERM)
        second = self.wait_event('child_started', first['monotonic'])
        self.assertNotEqual(first['child_pid'], second['child_pid'])
        self.assertLess(second['monotonic'] - first['monotonic'], 6)
        self.stop(process)
        self.assertEqual(process.returncode, 0)
        stopped = self.wait_event('monitor_stopped')
        self.assertEqual(stopped['child_pid'], second['child_pid'])

    def test_second_monitor_cannot_start_same_working_directory(self):
        self.start()
        first = self.wait_event('child_started')
        duplicate = self.start()
        self.assertEqual(duplicate.wait(timeout=10), 2)
        self.assertEqual(self.wait_event('child_started')['child_pid'], first['child_pid'])

    def test_missing_database_configuration_refuses_start_without_leaking_values(self):
        secret_marker = 'private-synthetic-marker'
        self.env_file.write_text(json.dumps({'SQL_DSN': secret_marker}), encoding='utf-8')
        process = self.start()
        self.assertEqual(process.wait(timeout=10), 2)
        text = self.events.read_text(encoding='utf-8')
        self.assertNotIn(secret_marker, text)
        self.assertNotIn('child_started', text)

    def test_wrong_candidate_hash_refuses_start(self):
        process = self.start(['--expected-sha256', hashlib.sha256(b'wrong').hexdigest()])
        self.assertEqual(process.wait(timeout=10), 2)
        self.assertNotIn('child_started', self.events.read_text(encoding='utf-8'))


if __name__ == '__main__':
    unittest.main()
