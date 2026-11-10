import os,pathlib,subprocess,sqlite3,shutil,json,time,urllib.request,hashlib
os.umask(0o077)
r='release-20261108-203718';d=pathlib.Path('/opt/cc-manager/releases')/r;b=pathlib.Path('/var/backups/cc-manager')/r;cfg=pathlib.Path('/etc/cc-manager/backups')/r
rollback=r'''#!/usr/bin/env python3
import pathlib,subprocess,sqlite3,shutil,sys,tempfile,os,hashlib
os.umask(0o077)
b=pathlib.Path(__file__).resolve().parent
cfg=pathlib.Path('/etc/cc-manager/backups')/b.name
snapshot=b/'before-upgrade.db'
def validate(p):
 c=sqlite3.connect(p);assert c.execute('pragma integrity_check').fetchone()[0]=='ok';c.close()
validate(snapshot)
if '--check' in sys.argv:
 with tempfile.TemporaryDirectory(dir=b) as temp:
  target=pathlib.Path(temp)/'restored.db';shutil.copy2(snapshot,target);validate(target)
  assert hashlib.sha256(snapshot.read_bytes()).digest()==hashlib.sha256(target.read_bytes()).digest()
 print('rollback snapshot restore check: ok');sys.exit(0)
subprocess.run(['systemctl','stop','cc-manager'],check=True)
if '--restore-db' in sys.argv:
 target=pathlib.Path('/var/lib/cc-manager/cc-manager.db')
 for suffix in ['-wal','-shm']:
  pathlib.Path(str(target)+suffix).unlink(missing_ok=True)
 shutil.copy2(snapshot,target);shutil.chown(target,user='cc-manager',group='cc-manager');target.chmod(0o600)
for name,target in [('cc-manager.env','/etc/cc-manager/cc-manager.env'),('cc-manager.service','/etc/systemd/system/cc-manager.service')]:shutil.copy2(cfg/name,target)
link=pathlib.Path('/opt/cc-manager/rollback.next');link.unlink(missing_ok=True);link.symlink_to((b/'previous-release').read_text().strip());link.replace('/opt/cc-manager/current')
subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','start','cc-manager'],check=True)
print('previous release restored; database restored:', '--restore-db' in sys.argv)
'''
(b/'rollback.py').write_text(rollback);(b/'rollback.py').chmod(0o700)
old=pathlib.Path('/opt/cc-manager/current').resolve(); meta={'previous_release':str(old),'previous_binary_sha256':hashlib.sha256((old/'cc-manager').read_bytes()).hexdigest(),'new_binary_sha256':hashlib.sha256((d/'cc-manager').read_bytes()).hexdigest()}
started=time.monotonic();subprocess.run(['systemctl','stop','cc-manager'],check=True)
try:
 source=sqlite3.connect('/var/lib/cc-manager/cc-manager.db');dest=sqlite3.connect(b/'before-upgrade.db');source.backup(dest);dest.close()
 tables=[x[0] for x in source.execute("select name from sqlite_master where type='table' and name not like 'sqlite_%'")]
 meta['before_counts']={t:source.execute('select count(*) from "'+t+'"').fetchone()[0] for t in tables};source.close()
 subprocess.run([str(b/'rollback.py'),'--check'],check=True)
 envpath=pathlib.Path('/etc/cc-manager/cc-manager.env');original=envpath.read_text();lines=[x for x in original.splitlines() if not x.startswith('CC_MANAGER_TRUST_PROXY=')];envpath.write_text('\n'.join(lines)+'\nCC_MANAGER_TRUST_PROXY=1\n');envpath.chmod(0o600)
 shutil.copy2(d/'deploy/production/cc-manager.service','/etc/systemd/system/cc-manager.service');os.chmod('/etc/systemd/system/cc-manager.service',0o644)
 link=pathlib.Path('/opt/cc-manager/current.next');link.symlink_to(d);link.replace('/opt/cc-manager/current')
 subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','start','cc-manager'],check=True)
 for i in range(30):
  try:
   with urllib.request.urlopen('http://127.0.0.1:18080/healthz',timeout=2) as resp:h=json.load(resp)
   assert h['ok'] and h['database']=='ok' and h['version']=='deploy-20261108'
   break
  except Exception:time.sleep(1)
 else:raise RuntimeError('new release health check failed')
 meta['downtime_seconds']=round(time.monotonic()-started,2);meta['health']=h
 (b/'upgrade.json').write_text(json.dumps(meta,indent=2)+'\n');print(json.dumps(meta,indent=2))
except Exception:
 subprocess.run([str(b/'rollback.py'),'--restore-db'],check=True)
 raise
