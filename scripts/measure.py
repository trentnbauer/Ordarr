#!/usr/bin/env python3
"""Sample the application process directly; nested Docker cgroup stats may be shared."""
import argparse
import json
import os
import subprocess
import time

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--container', default='ordarr-ordarr-1')
p.add_argument('--image', default='ordarr:0.7.0-dev')
a = p.parse_args()
env = dict(os.environ)
for key in ('DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_TLS', 'DOCKER_TLS_VERIFY', 'DOCKER_CERT_PATH'):
    env.pop(key, None)
docker = ['docker', '--host=unix:///var/run/docker.sock']
def output(*args):
    return subprocess.check_output(docker + list(args), env=env, text=True).strip()
def ticks():
    # Fields 14 and 15 follow a comm field which can contain spaces.
    fields = output('exec', a.container, 'cat', '/proc/1/stat').rsplit(')', 1)[1].split()
    return int(fields[11]) + int(fields[12])

samples = []
for _ in range(3):
    before = ticks()
    start = time.monotonic()
    time.sleep(5)
    after = ticks()
    elapsed = time.monotonic() - start
    samples.append(round(100 * (after - before) / os.sysconf('SC_CLK_TCK') / elapsed, 3))
status = output('exec', a.container, 'cat', '/proc/1/status')
rss = int(next(line.split()[1] for line in status.splitlines() if line.startswith('VmRSS:')))
print(json.dumps({
    'image_bytes': int(output('image', 'inspect', a.image, '--format', '{{.Size}}')),
    'process_rss_kib': rss,
    'cpu_percent_of_one_core_5s_samples': samples,
    'container_cgroup': output('exec', a.container, 'cat', '/proc/1/cgroup'),
}, indent=2))
