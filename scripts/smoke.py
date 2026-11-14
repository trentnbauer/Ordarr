#!/usr/bin/env python3
"""Exercise a real instance, including persistence after process/container replacement."""
import argparse
import http.cookiejar
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--compose', action='store_true', help='use an isolated Compose project and named volume')
args = parser.parse_args()
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    port = sock.getsockname()[1]
base = f'http://127.0.0.1:{port}'
jar = http.cookiejar.CookieJar()

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(jar), NoRedirect())
def request(path, form=None):
    data = None if form is None else urllib.parse.urlencode(form).encode()
    req = urllib.request.Request(base + path, data=data)
    if form is not None:
        req.add_header('Origin', base)
    try:
        res = client.open(req, timeout=5)
    except urllib.error.HTTPError as exc:
        res = exc
    with res:
        return res.status, res.headers, res.read().decode()

def csrf(body):
    return re.search(r'name="csrf" value="([a-f0-9]{64})"', body).group(1)

def expect(actual, expected):
    if actual != expected:
        raise AssertionError(f'got {actual!r}; expected {expected!r}')

project = 'ordarr-smoke-' + secrets.token_hex(4)
env = dict(os.environ, ORDARR_PORT=str(port), ORDARR_BIND_IP='127.0.0.1', ORDARR_SECURE_COOKIES='false')
# Always use the managed local daemon without altering Docker registry configuration.
for key in ('DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_TLS', 'DOCKER_TLS_VERIFY', 'DOCKER_CERT_PATH'):
    env.pop(key, None)
compose = ['docker', '--host=unix:///var/run/docker.sock', 'compose', '-p', project]
process = None

def command(*suffix):
    subprocess.run(compose + list(suffix), cwd=ROOT, env=env, check=True)

with tempfile.TemporaryDirectory(prefix='ordarr-smoke-') as tmp:
    log = open(Path(tmp) / 'server.log', 'w+')
    def start():
        global process
        if args.compose:
            command('up', '-d', '--no-build', '--wait', '--wait-timeout', '60')
        else:
            local_env = dict(os.environ, ORDARR_ADDR=f'127.0.0.1:{port}', ORDARR_DATA_DIR=tmp, ORDARR_SECURE_COOKIES='false')
            process = subprocess.Popen([str(ROOT / 'bin/ordarr')], cwd=ROOT, env=local_env, stdout=log, stderr=log)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            try:
                if request('/healthz')[0] == 200:
                    return
            except (OSError, urllib.error.URLError):
                pass
            if process is not None and process.poll() is not None:
                raise RuntimeError('server exited during startup')
            time.sleep(.1)
        raise RuntimeError('server never became healthy')

    def read_key():
        if args.compose:
            key = subprocess.check_output(compose + ['exec', '-T', 'ordarr', 'cat', '/data/credentials.key'], cwd=ROOT, env=env)
            mode = subprocess.check_output(compose + ['exec', '-T', 'ordarr', 'stat', '-c', '%a', '/data/credentials.key'], cwd=ROOT, env=env, text=True).strip()
            expect(mode, '600')
        else:
            key = (Path(tmp) / 'credentials.key').read_bytes()
            expect((Path(tmp) / 'credentials.key').stat().st_mode & 0o777, 0o600)
        expect(len(key), 32)
        return key

    def stop():
        global process
        if args.compose:
            command('stop', '-t', '15')
            container = subprocess.check_output(compose + ['ps', '-a', '-q', 'ordarr'], cwd=ROOT, env=env, text=True).strip()
            code = subprocess.check_output(['docker', '--host=unix:///var/run/docker.sock', 'inspect', '--format', '{{.State.ExitCode}}', container], env=env, text=True).strip()
            expect(code, '0')
            logs = subprocess.check_output(compose + ['logs', '--no-color', 'ordarr'], cwd=ROOT, env=env, text=True)
            assert 'Ordarr stopped gracefully' in logs
            command('rm', '-f')  # Replace container, retain volume.
        elif process is not None:
            process.terminate()
            expect(process.wait(timeout=15), 0)
            process = None

    try:
        start()
        saved_key = read_key()
        status, headers, _ = request('/')
        expect((status, headers['Location']), (303, '/setup'))
        status, _, body = request('/setup')
        expect(status, 200)
        password = secrets.token_urlsafe(24)
        status, headers, _ = request('/setup', {'csrf': csrf(body), 'username': 'smoke-admin', 'password': password})
        expect((status, headers['Location']), (303, '/'))
        status, _, body = request('/settings')
        expect(status, 200)
        assert 'smoke-admin' in body
        export_status, export_headers, exported = request('/settings/export.env')
        expect(export_status, 200)
        assert 'ordarr.env' in export_headers['Content-Disposition']
        assert "ORDARR_SECURE_COOKIES='false'" in exported
        assert password not in exported
        if args.compose:
            assert f"ORDARR_PORT='{port}'" in exported
            assert "ORDARR_BIND_IP='127.0.0.1'" in exported
        status, headers, _ = request('/collections', {'csrf': csrf(body), 'title': 'Persistent mixed order', 'description': 'Keep deliberate repeats'})
        expect(status, 303)
        collection_path = headers['Location']
        def add_entry(kind, title, provider_id, **extra):
            status, _, form = request(collection_path)
            expect(status, 200)
            revision = re.search(r'name="revision" value="([0-9]+)"', form).group(1)
            values = {'csrf': csrf(form), 'revision': revision, 'action': 'add', 'kind': kind, 'entry_title': title, 'provider': 'tmdb', 'external_id': provider_id}
            values.update(extra)
            expect(request(collection_path, values)[0], 303)
        add_entry('movie', 'Opening movie', '1771')
        add_entry('episode_range', 'Series specials', '83867', season='0', start='1', end='3')
        add_entry('movie', 'Opening movie', '1771')
        status, _, collection_body = request(collection_path)
        expect(status, 200)
        entry_ids = re.findall(r'data-entry-id="([a-f0-9]+)"', collection_body)
        expect(len(entry_ids), 5)
        expect(len(set(entry_ids)), 5)
        session = next(c.value for c in jar if c.name == 'ordarr_session')
        stop()
        start()
        if read_key() != saved_key:
            raise AssertionError("credential key changed after replacement")
        status, _, body = request('/settings')
        expect(status, 200)  # Persisted session and administrator survive replacement.
        assert 'smoke-admin' in body
        status, _, persisted = request(collection_path)
        expect(status, 200)
        expect(re.findall(r'data-entry-id="([a-f0-9]+)"', persisted), entry_ids)
        expect(persisted.count('<h3>Opening movie</h3>'), 2)
        expect(persisted.count('<h3>Series specials</h3>'), 3)
        status, _, _ = request('/logout', {'csrf': csrf(body)})
        expect(status, 303)
        expect(request('/settings')[0], 303)
        # Replay of the revoked cookie must fail.
        req = urllib.request.Request(base + '/settings', headers={'Cookie': 'ordarr_session=' + session})
        replay = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        try:
            replay.open(req, timeout=5)
            raise AssertionError('revoked session accepted')
        except urllib.error.HTTPError as exc:
            expect(exc.code, 303)
        status, _, body = request('/login')
        expect(status, 200)
        status, _, _ = request('/login', {'csrf': csrf(body), 'username': 'smoke-admin', 'password': password})
        expect(status, 303)
        expect(request('/settings')[0], 200)
        stop()
        if not args.compose:
            log.seek(0)
            assert log.read().count('Ordarr stopped gracefully') == 2
        print('PASS: health, Compose environment export, setup, private credential key persistence, authenticated settings, mixed collection and repeat persistence after replacement, logout, revoked-cookie rejection, login, graceful stop')
    finally:
        if args.compose:
            command('down', '--volumes', '--remove-orphans')
        elif process is not None:
            process.terminate()
            process.wait(timeout=15)
        log.close()
