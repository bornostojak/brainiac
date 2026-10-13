#!/usr/bin/env python3
"""Merge Brainiac HTTP MCP into Claude Code or OpenCode user configuration."""
import argparse
import datetime
import json
import os
from pathlib import Path
import re
import shutil
import stat
import tempfile
from urllib.parse import urlsplit


def tokens(text):
    """JSONC lexer retaining spans; comments and string escapes are distinct."""
    result = []
    pos = 0
    decoder = json.JSONDecoder()
    while pos < len(text):
        if text[pos].isspace():
            pos += 1
            continue
        if text.startswith('//', pos):
            end = text.find('\n', pos)
            pos = len(text) if end < 0 else end + 1
            continue
        if text.startswith('/*', pos):
            end = text.find('*/', pos + 2)
            if end < 0:
                raise ValueError('Unclosed JSONC comment')
            pos = end + 2
            continue
        start = pos
        if text[pos] == '"':
            _, size = decoder.raw_decode(text[pos:])
            pos += size
        elif text[pos] in '{}[]:,':
            pos += 1
        else:
            match = re.match(r'(?:true|false|null|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)', text[pos:])
            if not match:
                raise ValueError('Invalid JSONC token')
            pos += len(match.group(0))
        result.append((text[start:pos], start, pos))
    return result


def parse_jsonc(text):
    items = tokens(text)
    clean = ' '.join(raw for i, (raw, _, _) in enumerate(items)
                     if not (raw == ',' and i + 1 < len(items) and items[i + 1][0] in (']', '}')))
    value = json.loads(clean)
    if not isinstance(value, dict):
        raise ValueError('Configuration must be a JSON object')
    return value, items


def replace_top_level(text, items, key, value):
    """Preserve comments and formatting outside the one changed top-level key."""
    depth = 0
    for i, (raw, _, _) in enumerate(items):
        if depth == 1 and raw.startswith('"') and json.loads(raw) == key and items[i + 1][0] == ':':
            start = items[i + 2][1]
            j = i + 2
            if items[j][0] in ('{', '['):
                level = 0
                while j < len(items):
                    if items[j][0] in ('{', '['):
                        level += 1
                    elif items[j][0] in ('}', ']'):
                        level -= 1
                    if level == 0:
                        break
                    j += 1
            end = items[j][2]
            return text[:start] + json.dumps(value, indent=2) + text[end:]
        if raw in ('{', '['):
            depth += 1
        elif raw in ('}', ']'):
            depth -= 1
    closing = items[-1][1]
    comma = ',' if len(items) > 2 and items[-2][0] != ',' else ''
    return text[:closing] + comma + '\n  ' + json.dumps(key) + ': ' + json.dumps(value, indent=2) + '\n' + text[closing:]


def configure(harness, base, config_path=None):
    parsed = urlsplit(base)
    if parsed.scheme not in ('http', 'https') or not parsed.netloc or parsed.username or parsed.query or parsed.fragment or parsed.path not in ('', '/'):
        raise ValueError('Use an HTTP(S) service origin')
    if harness == 'claude':
        # Claude Code user-scoped MCP lives in .claude.json, not its project .mcp.json.
        config_dir = os.environ.get('CLAUDE_CONFIG_DIR')
        path = Path(config_dir) / '.claude.json' if config_dir else Path.home() / '.claude.json'
        key = 'mcpServers'
        entry = {'type': 'http', 'url': base.rstrip('/') + '/mcp'}
    else:
        directory = Path(os.environ.get('XDG_CONFIG_HOME', str(Path.home() / '.config'))) / 'opencode'
        explicit = os.environ.get('OPENCODE_CONFIG')
        path = Path(explicit) if explicit else directory / ('opencode.jsonc' if (directory / 'opencode.jsonc').exists() else 'opencode.json')
        key = 'mcp'
        entry = {'type': 'remote', 'url': base.rstrip('/') + '/mcp', 'enabled': True, 'oauth': False}
    if config_path is not None:
        path = Path(config_path)
    original = path.read_text() if path.exists() else '{}\n'
    document, items = parse_jsonc(original)
    servers = document.get(key, {})
    if not isinstance(servers, dict):
        raise ValueError(key + ' must be an object')
    if servers.get('brainiac') == entry:
        print('Brainiac MCP already configured for ' + harness)
        return
    servers['brainiac'] = entry
    updated = replace_top_level(original, items, key, servers)
    parse_jsonc(updated)  # Validate before writing.
    path.parent.mkdir(parents=True, exist_ok=True)
    mode = stat.S_IMODE(path.stat().st_mode) if path.exists() else 0o600
    if path.exists():
        suffix = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
        shutil.copy2(path, str(path) + '.brainiac-backup-' + suffix)
    fd, temporary = tempfile.mkstemp(prefix=path.name + '.', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as handle:
            os.fchmod(handle.fileno(), mode)
            handle.write(updated)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    print('Configured Brainiac MCP for ' + harness + ' at ' + str(path))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('harness', choices=('claude', 'opencode'))
    parser.add_argument('--url', required=True)
    args = parser.parse_args()
    try:
        configure(args.harness, args.url)
    except (OSError, ValueError) as error:
        parser.exit(1, str(error) + '\n')
