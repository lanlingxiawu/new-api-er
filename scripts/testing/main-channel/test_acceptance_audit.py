"""Offline regression fixtures; no servers, credentials or provider requests."""
import ast
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest


HERE = Path(__file__).parent


class FailureClassificationTest(unittest.TestCase):
    def test_main_is_not_its_own_candidate_peer(self):
        main = {'layer': 'main', 'spec': 'chat_sse', 'case': 'cache_usage',
                'strict': False, 'error': 'wire and ledger cache-read differ'}
        candidate = dict(main, layer='candidate')
        candidate_only = dict(candidate, case='normal')
        report = {'status': 'failed', 'records': [],
                  'failures': [main, candidate, candidate_only]}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'result.json'
            original = json.dumps(report).encode()
            path.write_bytes(original)
            subprocess.run([sys.executable, str(HERE / 'classify_parity.py'), str(path)],
                           check=True, capture_output=True, text=True)
            result = json.loads(path.with_name('failure-classification.json').read_text())
            self.assertEqual(path.read_bytes(), original)
        self.assertEqual(result['original_status'], 'failed')
        self.assertEqual([row['classification'] for row in result['findings']],
                         ['official_main_failure', 'also_observed_in_official_main', 'unresolved'])


class MatrixAccountingTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.report = Path(self.tmp.name)
        (self.report / 'body').write_bytes(b'data: [DONE]\n\n')
        # Load only pure validation functions. The production CLI's module
        # initialization reads private deployment state and must not run here.
        source = HERE / 'matrix.py'
        tree = ast.parse(source.read_text(encoding='utf-8'))
        selected = ast.Module(body=[node for node in tree.body
                                    if isinstance(node, ast.FunctionDef) and node.name in
                                    {'parsed', 'error_response', 'comparable_stream', 'validate_records'}],
                              type_ignores=[])
        self.namespace = {'json': json, 're': re, 'REPORT': self.report, 'STARTED_AT': 0,
                          'audit_records': lambda: [], 'websocket_logs': lambda *_: []}
        exec(compile(selected, str(source), 'exec'), self.namespace)

    def validate(self, case, mode, main_logs, candidate_logs, strict=False):
        records = [dict(layer=layer, spec='realtime_ws' if mode == 'ws' else 'chat_sse',
                        case=case, mode=mode, strict=strict, started_at=1, finished_at=2,
                        status=200, body_file='body', wire_usage=[], logs=logs)
                   for layer, logs in [('main', main_logs), ('candidate', candidate_logs)]]
        failures = []
        checks = self.namespace['validate_records'](records, [{'name': 'main', 'log_cache': []}], failures)
        return checks, [row for row in failures if 'equal-depth main persisted accounting' in row['error']]

    def test_all_disabled_cases_compare_actual_accounting(self):
        for case in ['normal', 'duplicate_terminal', 'invalid_json', 'missing_terminal']:
            for mode in ['sse', 'ws']:
                with self.subTest(case=case, mode=mode):
                    main = [{'type': 2, 'prompt_tokens': 303, 'completion_tokens': 51, 'quota': 354}]
                    candidate = [{'type': 2, 'prompt_tokens': 101, 'completion_tokens': 17, 'quota': 118}]
                    _, failures = self.validate(case, mode, main, candidate)
                    self.assertEqual(len(failures), 1)

    def test_missing_and_duplicate_rows_are_differences(self):
        charge = {'type': 2, 'prompt_tokens': 1, 'completion_tokens': 1, 'quota': 2}
        for candidate in [[], [charge, charge], [dict(charge, type=5)]]:
            with self.subTest(candidate=candidate):
                _, failures = self.validate('invalid_json', 'sse', [charge], candidate)
                self.assertEqual(len(failures), 1)

    def test_equal_rows_ignore_generated_metadata(self):
        charge = {'type': 2, 'prompt_tokens': 1, 'completion_tokens': 1, 'quota': 2}
        checks, failures = self.validate('duplicate_terminal', 'ws', [dict(charge, id=1)], [dict(charge, id=2)])
        self.assertEqual(failures, [])
        self.assertEqual(checks.get('protocol_disabled_ledger_parity'), 1)

    def test_strict_policy_does_not_require_main_accounting(self):
        _, failures = self.validate('duplicate_terminal', 'ws', [{'type': 2, 'quota': 354}], [], strict=True)
        self.assertEqual(failures, [])


if __name__ == '__main__':
    unittest.main()
