#!/usr/bin/env python3
"""Exercise discovery and JSONC MCP merges without touching installed harnesses."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


discover = load('discovery', ROOT / 'skills/progress-tracker/scripts/discover.py')
config = load('mcp_config', ROOT / 'skills/progress-tracker/scripts/configure_mcp.py')


class SkillChecks(unittest.TestCase):
    def test_jsonc_merge_preserves_unrelated_config_and_is_idempotent(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'opencode.jsonc'
            original = '// global comment\n{\n "model": "provider/model", // preserve this\n "mcp": {"other": {"url": "http://example.com/a//b",},},\n "nested": {"mcp": "leave alone"},\n}\n'
            path.write_text(original)
            config.configure('opencode', 'http://127.0.0.1:3011', path)
            updated = path.read_text()
            doc, _ = config.parse_jsonc(updated)
            self.assertEqual(doc['mcp']['other']['url'], 'http://example.com/a//b')
            self.assertEqual(doc['nested']['mcp'], 'leave alone')
            self.assertIn('// preserve this', updated)
            self.assertIn('// global comment', updated)
            self.assertEqual(doc['mcp']['brainiac']['url'], 'http://127.0.0.1:3011/mcp')
            self.assertEqual(len(list(path.parent.glob('*.brainiac-backup-*'))), 1)
            config.configure('opencode', 'http://127.0.0.1:3011', path)
            self.assertEqual(path.read_text(), updated)
            self.assertEqual(len(list(path.parent.glob('*.brainiac-backup-*'))), 1)

    def test_claude_merge_and_invalid_config(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'claude.json'
            path.write_text('{"projects":{"a":{"allowedTools":["read"]}},"mcpServers":{"existing":{"type":"stdio","command":"example"}}}')
            config.configure('claude', 'http://localhost:8888', path)
            doc = json.loads(path.read_text())
            self.assertEqual(doc['mcpServers']['brainiac']['type'], 'http')
            self.assertEqual(doc['mcpServers']['existing']['command'], 'example')
            self.assertEqual(doc['projects']['a']['allowedTools'], ['read'])
            path.write_text('{broken')
            with self.assertRaises(ValueError):
                config.configure('claude', 'http://localhost:8888', path)
            self.assertEqual(path.read_text(), '{broken')

    def test_discovery_is_authoritative_and_only_ensure_creates(self):
        requests = []
        projects = []
        def fake(base, path, payload=None):
            self.assertEqual(base, 'http://explicit:9999')
            requests.append((path, payload))
            if path == '/healthz':
                return {'service': 'brainiac', 'apiVersion': 'v1', 'status': 'ok'}
            if path.startswith('/api/v1/projects?'):
                return {'projects': projects}
            if path == '/api/v1/projects':
                project = {'id': 'stable-project'}
                projects.append(project)
                return project
            if path.startswith('/api/v1/notifications'):
                return {'latestCursor': 15}
            raise AssertionError(path)
        original_request = discover.request
        original_url = os.environ.get('PROGRESS_TRACKER_URL')
        original_id = os.environ.pop('PROGRESS_PROJECT_ID', None)
        try:
            os.environ['PROGRESS_TRACKER_URL'] = 'http://explicit:9999'
            discover.request = fake
            args = argparse.Namespace(workspace=str(ROOT), name='Test', project_id=None, ensure=False)
            self.assertTrue(discover.discover(args)['needsCreate'])
            self.assertFalse(any(payload for _, payload in requests))
            args.ensure = True
            self.assertEqual(discover.discover(args)['projectId'], 'stable-project')
            self.assertEqual(discover.discover(args)['projectId'], 'stable-project')
            self.assertEqual(sum(payload is not None for _, payload in requests), 1)
            args.project_id = '../knowledge/example'
            with self.assertRaises(ValueError):
                discover.discover(args)
            args.project_id = None
            discover.request = lambda *a, **kw: (_ for _ in ()).throw(OSError('offline'))
            result = discover.discover(args)
            self.assertEqual(result['backend'], 'legacy')
            self.assertEqual(result['checkedUrl'], 'http://explicit:9999')
        finally:
            discover.request = original_request
            if original_url is None:
                os.environ.pop('PROGRESS_TRACKER_URL', None)
            else:
                os.environ['PROGRESS_TRACKER_URL'] = original_url
            if original_id is not None:
                os.environ['PROGRESS_PROJECT_ID'] = original_id


if __name__ == '__main__':
    unittest.main()
