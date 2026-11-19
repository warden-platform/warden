"""Shared local configuration reader for demo scripts.

Mirrors the warden-control contract: process environment > .env > config.yaml,
with ${WARDEN_*} placeholders resolved against .env and var/warden/secrets.json.
Runs on the system python3 without PyYAML (falls back to a minimal parser for
this repository's two-level config.yaml shape).
"""
import json
import os
import re
from urllib.parse import quote, urlparse

try:
    import yaml
except ModuleNotFoundError:
    yaml = None

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def load_config_yaml(path: str) -> dict:
    with open(path) as handle:
        text = handle.read()
    if yaml is not None:
        return yaml.safe_load(text) or {}
    settings, section = {}, None
    for raw in text.splitlines():
        line = raw.rstrip()
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        if not line.startswith(' ') and line.endswith(':'):
            section = line[:-1].strip()
            settings[section] = {}
            continue
        if section and ':' in line:
            key, _, value = line.strip().partition(':')
            value = value.strip().strip('"\'')
            if value.lower() in ('true', 'false'):
                value = value.lower() == 'true'
            elif value.lstrip('-').isdigit():
                value = int(value)
            settings[section][key.strip()] = value
    return settings


def _dotenv_values() -> dict:
    values = {}
    env_path = os.path.join(ROOT, '.env')
    if os.path.exists(env_path):
        with open(env_path) as config:
            for raw_line in config:
                line = raw_line.strip()
                if not line or line.startswith('#'):
                    continue
                line = line.removeprefix('export ').lstrip()
                key, separator, raw_value = line.partition('=')
                if separator and key.strip():
                    values[key.strip()] = raw_value.strip().strip('"\'')
    return values


def _resolve_placeholders(value: str, dotenv: dict) -> str:
    secrets = {}
    secrets_path = os.path.join(ROOT, 'var', 'warden', 'secrets.json')
    if os.path.exists(secrets_path):
        with open(secrets_path) as handle:
            secrets = json.load(handle)

    def lookup(match):
        name = match.group(1)
        return os.environ.get(name) or dotenv.get(name) or secrets.get(name) or ''

    return re.sub(r'\$\{([A-Z_][A-Z0-9_]*)\}', lookup, value)


def local_setting(name: str) -> str:
    if os.environ.get(name):
        return os.environ[name]
    dotenv = _dotenv_values()
    if dotenv.get(name):
        return dotenv[name]
    config_path = os.path.join(ROOT, 'config.yaml')
    if os.path.exists(config_path):
        settings = load_config_yaml(config_path)
        flattened = {}
        for section, values in settings.items():
            if isinstance(values, dict):
                for key, value in values.items():
                    flattened[f"{section}_{key}".upper()] = str(value)
        if name in flattened:
            return _resolve_placeholders(flattened[name], dotenv)
    return ''


def database_url() -> str:
    url = local_setting('DATABASE_URL')
    username = local_setting('DATABASE_USERNAME')
    password_key = 'DATABASE_PASSWORD'
    password = local_setting(password_key)
    if not url:
        raise SystemExit('set database.url in config.yaml')
    parsed = urlparse(url)
    if username:
        credentials = f"{quote(username, safe='')}:{quote(password, safe='')}@" if password else f"{quote(username, safe='')}@"
        parsed = parsed._replace(netloc=credentials + parsed.netloc)
    return parsed.geturl()