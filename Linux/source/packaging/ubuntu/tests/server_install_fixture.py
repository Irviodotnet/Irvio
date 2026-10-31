#!/usr/bin/env python3
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me
"""Run only in the documented private systemd mount namespace; no real daemon/firewall changes."""
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import tempfile

assert os.geteuid() == 0 and os.environ.get("IRVIO_INSTALL_FIXTURE") == "1"
for path in ("/opt", "/etc/irvio", "/etc/systemd/system", "/var/lib/irvio", "/usr/local/bin", "/run/irvio"):
    assert subprocess.check_output(["findmnt", "-n", "-o", "FSTYPE", "--target", path], text=True).strip() == "tmpfs", path
source = pathlib.Path(__file__).resolve().parents[1]
root = pathlib.Path(tempfile.mkdtemp(prefix="irvio-install-fixture-"))
package = root / "package"
shutil.copytree(source, package)
for d in ("payload/bin", "payload/geo", "licenses", "payload/ui"):
    (package / d).mkdir(parents=True, exist_ok=True)
shutil.copy2("/bin/true", package / "payload/bin/irviod")
(package / "payload/geo/country.mmdb").write_bytes(b"fixture-not-real-geoip")
(package / "payload/geo/ATTRIBUTION.md").write_text("fixture")
(package / "BUILD_INFO.json").write_text("{}")
for name in ("install.sh", "uninstall.sh", "scripts/check-server.sh"):
    (package / name).chmod(0o755)
(package / "SHA256SUMS").write_text("".join(hashlib.sha256(p.read_bytes()).hexdigest()+"  ./"+str(p.relative_to(package))+"\n" for p in sorted(package.rglob("*")) if p.is_file() and p.name != "SHA256SUMS"))
mock = root / "mock"
mock.mkdir()
dispatcher = mock / "dispatcher"
dispatcher.write_text('''#!/usr/bin/python3
import os,sys,json,pathlib
n=pathlib.Path(sys.argv[0]).name;a=sys.argv[1:];state=pathlib.Path(os.environ['FIXTURE_STATE']);live=(state/'live').exists()
with (state/'commands').open('a') as f:f.write(json.dumps([n]+a)+'\\n')
if n=='systemctl':
 if a[0]=='show':
  key=a[a.index('-p')+1]
  print({'LoadState':'not-found','InvocationID':'a'*32,'MainPID':'777'}.get(key,''));sys.exit(0)
 if a[0]=='is-active':sys.exit(0 if a[-1]=='irvio-server.service' and live and os.environ.get('FAIL_START')!='1' else 3)
 if a[0]=='is-failed':sys.exit(0 if os.environ.get('FAIL_START')=='1' else 1)
 if a[0]=='enable':(state/'live').touch()
 if a[0]=='disable':(state/'live').unlink(missing_ok=True)
elif n=='nft':
 if a[:2]==['list','tables']:
  if os.environ.get('CUSTOM_FW')=='1':print('table inet custom')
  elif os.environ.get('ACTIVE_UFW')=='1':print('table ip filter')
 elif a[:2]==['list','ruleset']:print('drop-safety-v0-tcp drop-safety-v0-udp')
elif n=='ufw':
 if a==['status']:print('Status: active' if os.environ.get('ACTIVE_UFW')=='1' else 'Status: inactive')
elif n in ('iptables-save','ip6tables-save'):pass
elif n=='pgrep':sys.exit(1)
elif n=='ip':sys.exit(1)
elif n=='ss':
 if '-luntp' in a and live:
  for proto,port in [('tcp',4000),('udp',3478),('udp',51820)]:print(f'{proto} LISTEN 0 0 0.0.0.0:{port} 0.0.0.0:* users:(("irviod",pid=777,fd=4))')
elif n=='journalctl':
 for e in [dict(msg='PRODUCT_RUNTIME_STARTED',role='server'),dict(msg='PUBLIC_STUN_READY'),dict(msg='PUBLIC_REACHABILITY_VERIFIED',family=1,expires_at='2999-01-01T00:00:00Z'),dict(msg='RENDEZVOUS_SERVICE_ACTIVE',geoip_degraded=False),dict(msg='EXIT_CAPABILITY_READY'),dict(msg='RENDEZVOUS_REGISTRATIONS',active=2,closed=False)]:print(json.dumps(e))
else:sys.exit(91)
''')
dispatcher.chmod(0o755)
for name in ("systemctl", "nft", "ufw", "iptables-save", "ip6tables-save", "pgrep", "ip", "ss", "journalctl"):
    (mock / name).symlink_to(dispatcher)
env = dict(os.environ, PATH=str(mock)+":"+os.environ["PATH"], FIXTURE_STATE=str(root))

def run(args, ok=True, extra=None):
    p = subprocess.run(args, env=dict(env, **(extra or {})), text=True, capture_output=True)
    print(p.stdout, p.stderr)
    assert (p.returncode == 0) == ok, (args, p.returncode)
    return p

install = [str(package / "install.sh"), "--role", "server", "--bootstrap", "irvio-bootstrap:v1:CAEQ", "--no-deps"]
run(install, False, {"CUSTOM_FW": "1"})
assert not pathlib.Path('/opt/irvio').exists()
for flags in ({}, {"ACTIVE_UFW": "1"}, {"FAIL_START": "1"}):
    p=run(install, "FAIL_START" not in flags, flags)
    assert not pathlib.Path('/opt/irvio/ui').exists()
    assert not pathlib.Path('/etc/systemd/system/irvio.socket').exists()
    assert pathlib.Path('/etc/irvio/server.env').stat().st_mode & 0o777 == 0o600
    if 'FAIL_START' in flags:
        assert 'NOT READY' in p.stderr and 'journalctl' in p.stderr
    else:
        assert 'IRVIO server: OK' in p.stdout
    run(install, False, flags)
    pathlib.Path('/var/lib/irvio/preserve-identity').write_text('fixture')
    run(['/opt/irvio/uninstall.sh'], extra=flags)
    assert pathlib.Path('/var/lib/irvio/preserve-identity').read_text()=='fixture'
    assert pathlib.Path('/etc/irvio/server.env').exists()
    pathlib.Path('/etc/irvio/server.env').unlink()
commands=[json.loads(l) for l in (root/'commands').read_text().splitlines()]
assert ['ufw','allow','51820/udp','comment','IRVIO server'] in commands
assert ['ufw','route','allow','in','on','hydra0','comment','IRVIO Exit forwarding'] in commands
assert not any(c[0]=='ufw' and any(x in c for x in ('reset','enable','disable')) for c in commands)
print('PASS: private-filesystem server installation, UFW commands, readiness success/failure, reinstall refusal and profile-preserving uninstall. Services/firewall were fixtures.')
