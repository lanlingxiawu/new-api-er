"""Check report normalization without importing the private deployment state."""
import ast
import argparse
import base64
import json
from pathlib import Path
import re
import secrets
import socket
import struct
import threading
import urllib.parse
import unittest
from unittest.mock import patch

source = Path(__file__).with_name('matrix.py')
tree = ast.parse(source.read_text(encoding='utf-8'))
namespace = {'json': json, 're': re}
functions = ast.Module(body=[node for node in tree.body if isinstance(node, ast.FunctionDef)
                            and node.name in ('parsed', 'comparable_stream')], type_ignores=[])
exec(compile(functions, str(source), 'exec'), namespace)
compare = namespace['comparable_stream']


class WireComparisonTest(unittest.TestCase):
    def test_imported_policy_helper_does_not_parse_callers_cli(self):
        functions = ast.Module(body=[node for node in tree.body if isinstance(node, ast.FunctionDef)
                                    and node.name == 'strict'], type_ignores=[])
        parser = argparse.ArgumentParser()
        parser.add_argument('--fixed-policy', action='store_true')
        calls = []
        scope = {'parser': parser, '__name__': 'matrix', 'api': lambda *args: calls.append(args)}
        exec(compile(functions, str(source), 'exec'), scope)
        with patch('sys.argv', ['fix_wallet_audit.py', '--extended']):
            scope['strict']({'name': 'candidate'}, False)
        self.assertEqual(calls[0][2]['values'], {'enabled': 'false'})
        scope['__name__'] = '__main__'
        with patch('sys.argv', ['matrix.py', '--fixed-policy']):
            with self.assertRaises(ValueError):
                scope['strict']({'name': 'candidate'}, False)

    def test_websocket_client_captures_duplicate_terminal_frames(self):
        functions = ast.Module(body=[node for node in tree.body if isinstance(node, ast.FunctionDef)
                                    and node.name in ('parsed', 'ws_call')], type_ignores=[])
        scope = dict(json=json, base64=base64, secrets=secrets, socket=socket,
                     struct=struct, urllib=urllib)
        exec(compile(functions, str(source), 'exec'), scope)
        terminal = b'{"type":"response.done","response":{"usage":{"total_tokens":12}}}'
        listener = socket.socket()
        self.addCleanup(listener.close)
        listener.bind(('127.0.0.1', 0))
        listener.listen(1)
        errors = []

        def serve():
            try:
                connection, _ = listener.accept()
                with connection, connection.makefile('rb') as stream:
                    connection.settimeout(3)
                    while stream.readline() != b'\r\n':
                        pass
                    connection.sendall(b'HTTP/1.1 101 Switching Protocols\r\n\r\n')
                    head = stream.read(2)
                    stream.read(4 + (head[1] & 127))  # Small masked request.
                    frame = bytes([129, len(terminal)]) + terminal
                    # A text message may span multiple frames, with control
                    # frames between them. Compare complete messages in order.
                    split = len(terminal) // 2
                    first = bytes([1, split]) + terminal[:split]
                    last = bytes([128, len(terminal)-split]) + terminal[split:]
                    connection.sendall(first + b'\x8a\x00' + last + frame + b'\x88\x00')
            except Exception as exc:
                errors.append(exc)

        worker = threading.Thread(target=serve, daemon=True)
        worker.start()
        client = {'base': 'http://127.0.0.1:' + str(listener.getsockname()[1]), 'key': 'fixture'}
        status, _, body = scope['ws_call'](client, '/v1/realtime', {})
        worker.join(3)
        self.assertFalse(worker.is_alive())
        self.assertEqual(errors, [])
        self.assertEqual(status, 101)
        self.assertEqual(body.splitlines(), [terminal, terminal])

    def test_truncated_ids_are_dynamic_but_corruption_is_preserved(self):
        left = b'data: {"id":"fixture_0123456789abcdef",'
        right = b'data: {"id":"fixture_abcdef0123456789",'
        self.assertEqual(compare(left), compare(right))
        self.assertNotEqual(compare(left), compare(left + b'}'))

    def test_usage_error_and_event_order_are_preserved(self):
        self.assertNotEqual(compare(b'data: {"usage":1}'), compare(b'data: {"usage":2}'))
        self.assertNotEqual(compare(b'data: {"type":"error"}'), compare(b'data: {}'))
        self.assertNotEqual(compare(b'event: start\nevent: stop'), compare(b'event: stop\nevent: start'))

    def test_error_request_id_annotations_are_diagnostics(self):
        expected = {'error': {'message': 'Synthetic HTTP rejection', 'type': 'api_error', 'code': None}}
        for suffix in [' (request id: gateway-001)',
                       ' (request id: upstream-001) (request id: gateway-002)']:
            with self.subTest(suffix=suffix):
                actual = {'error': dict(expected['error'], message=expected['error']['message'] + suffix)}
                self.assertEqual(compare(json.dumps(expected).encode()), compare(json.dumps(actual).encode()))
                self.assertEqual(compare(b'data: ' + json.dumps(expected).encode()),
                                 compare(b'data: ' + json.dumps(actual).encode()))
        self.assertEqual(compare(b'{"type":"error","message":"busy (request id: gateway-001)"}'),
                         compare(b'{"type":"error","message":"busy"}'))

    def test_request_id_normalization_preserves_error_meaning_and_user_text(self):
        original = b'{"error":{"message":"busy (request id: gateway-001)","type":"api_error","code":429}}'
        for changed in [original.replace(b'busy', b'invalid credentials'),
                        original.replace(b'api_error', b'authentication_error'),
                        original.replace(b'429', b'500')]:
            with self.subTest(changed=changed):
                self.assertNotEqual(compare(original), compare(changed))
        for key in ['message', 'content', 'text']:
            plain = json.dumps({key: 'literal text'}).encode()
            annotated = json.dumps({key: 'literal text (request id: gateway-001)'}).encode()
            self.assertNotEqual(compare(plain), compare(annotated))
        self.assertNotEqual(compare(b'{"error":{"message":"(request id: gateway-001) is invalid"}}'),
                            compare(b'{"error":{"message":"is invalid"}}'))


if __name__ == '__main__':
    unittest.main()
