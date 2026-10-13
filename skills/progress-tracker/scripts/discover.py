#!/usr/bin/env python3
"""Bounded Brainiac discovery; only --ensure may create a project."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urlsplit
from urllib.request import Request, urlopen


def request(base, path, payload=None):
    data = json.dumps(payload).encode() if payload is not None else None
    headers = {'Accept': 'application/json'}
    if data is not None:
        headers['Content-Type'] = 'application/json'
    with urlopen(Request(base + path, data=data, headers=headers), timeout=3) as response:
        return json.load(response)


def discover(args):
    saved = Path(__file__).resolve().parent.parent / 'references' / 'server-url.txt'
    configured = os.environ.get('PROGRESS_TRACKER_URL')
    base = (configured if configured is not None else saved.read_text().strip() if saved.exists() else 'http://127.0.0.1:8765').rstrip('/')
    parsed = urlsplit(base)
    if parsed.scheme not in ('http', 'https') or not parsed.netloc or parsed.username or parsed.path or parsed.query or parsed.fragment:
        raise ValueError('PROGRESS_TRACKER_URL must be an HTTP(S) service origin')
    try:
        health = request(base, '/healthz')
        if health.get('service') != 'brainiac' or health.get('apiVersion') != 'v1' or health.get('status') != 'ok':
            return {'backend': 'legacy', 'reason': 'Service is not compatible Brainiac API v1', 'checkedUrl': base}
    except (OSError, ValueError, URLError) as error:
        return {'backend': 'legacy', 'reason': str(error), 'checkedUrl': base}
    project_id = args.project_id or os.environ.get('PROGRESS_PROJECT_ID')
    if project_id:
        project = request(base, '/api/v1/projects/' + project_id)
    else:
        workspace = str(Path(args.workspace).resolve())
        try:
            root = subprocess.check_output(['git', '-C', workspace, 'rev-parse', '--show-toplevel'], stderr=subprocess.DEVNULL, text=True, timeout=3).strip()
        except (OSError, subprocess.SubprocessError):
            root = workspace
        key = os.environ.get('PROGRESS_PROJECT_KEY') or root
        path = '/api/v1/projects?' + urlencode({'externalKey': key})
        projects = request(base, path)['projects']
        if not projects and args.ensure:
            try:
                project = request(base, '/api/v1/projects', {'name': args.name or Path(root).name, 'externalKey': key, 'actor': 'skill-discovery'})
            except HTTPError as error:
                if error.code != 409:
                    raise
                projects = request(base, path)['projects']
                if not projects:
                    raise
                project = projects[0]
        elif projects:
            project = projects[0]
        else:
            return {'backend': 'brainiac', 'serviceUrl': base, 'projectId': None, 'externalKey': key, 'needsCreate': True}
        project_id = project['id']
    cursor = request(base, '/api/v1/notifications?limit=1')['latestCursor']
    return {'backend': 'brainiac', 'serviceUrl': base, 'projectId': project_id, 'url': base + '/projects/' + project_id, 'api': base + '/api/v1/projects/' + project_id, 'readme': base + '/projects/' + project_id + '/README.md', 'mcp': base + '/mcp', 'cursor': cursor}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workspace', default=os.getcwd())
    parser.add_argument('--name')
    parser.add_argument('--project-id')
    parser.add_argument('--ensure', action='store_true')
    args = parser.parse_args()
    try:
        result = discover(args)
    except (OSError, ValueError, KeyError, URLError) as error:
        print(json.dumps({'error': str(error)}))
        return 2
    print(json.dumps(result))
    return 0


if __name__ == '__main__':
    sys.exit(main())
