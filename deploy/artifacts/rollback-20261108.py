#!/usr/bin/env python3
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
 target=pathlib.Path('/var/lib/cc-manager-b/cc-manager.db')
 for suffix in ['-wal','-shm']:
  pathlib.Path(str(target)+suffix).unlink(missing_ok=True)
 shutil.copy2(snapshot,target);shutil.chown(target,user='cc-manager',group='cc-manager');target.chmod(0o600)
for name,target in [('cc-manager.env','/etc/cc-manager/cc-manager-b.env'),('cc-manager.service','/etc/systemd/system/cc-manager.service')]:shutil.copy2(cfg/name,target)
link=pathlib.Path('/opt/cc-manager/rollback.next');link.unlink(missing_ok=True);link.symlink_to((b/'previous-release').read_text().strip());link.replace('/opt/cc-manager/current')
subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','start','cc-manager'],check=True)
print('previous release restored; database restored:', '--restore-db' in sys.argv)
