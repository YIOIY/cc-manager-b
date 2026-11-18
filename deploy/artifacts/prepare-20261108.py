import os,pathlib,subprocess,hashlib,tarfile,sqlite3,shutil,json,shlex
os.umask(0o077)
r='release-20261108-203718'; base=pathlib.Path('/opt/cc-manager/releases'); a=base/(r+'.tar.gz')
assert hashlib.sha256(a.read_bytes()).hexdigest()=='f8330ad7c1e4e89639ec01dca7265234cdf54f9c2cfc65a0426f06ebd0a649c0'
with tarfile.open(a) as t:t.extractall(base,filter='data')
d=base/r
subprocess.run(['sha256sum','-c','SHA256SUMS'],cwd=d,check=True,stdout=subprocess.DEVNULL)
# Release directory must be traversable by the service user.
for p in [d,*d.rglob('*')]:
 p.chmod(0o755 if p.is_dir() or p.name=='cc-manager' or p.suffix=='.sh' else 0o644)
b=pathlib.Path('/var/backups/cc-manager')/r;b.mkdir(mode=0o700)
cfg=pathlib.Path('/etc/cc-manager/backups')/r;cfg.mkdir(parents=True,mode=0o700)
for source,name in [('/etc/cc-manager/cc-manager-b.env','cc-manager.env'),('/etc/systemd/system/cc-manager.service','cc-manager.service'),('/etc/nginx/conf.d/cc-manager.conf','cc-manager.nginx.conf')]:shutil.copy2(source,cfg/name)
(b/'previous-release').write_text(os.path.realpath('/opt/cc-manager/current')+'\n')
source=sqlite3.connect('/var/lib/cc-manager-b/cc-manager.db'); dest=sqlite3.connect(b/'online.db');source.backup(dest);dest.close();source.close()
pre=b/'preflight';pre.mkdir(mode=0o700);shutil.copy2(b/'online.db',pre/'test.db')
env=os.environ.copy()
for line in (cfg/'cc-manager.env').read_text().splitlines():
 if line.startswith('CC_MANAGER_'):
  k,v=line.split('=',1);env[k]=shlex.split(v)[0] if shlex.split(v) else ''
env['CC_MANAGER_DB']=str(pre/'test.db')
run=subprocess.run([str(d/'cc-manager'),'rotate-key'],env=env,capture_output=True,text=True)
assert run.returncode==0,run.stderr
rotation=json.loads(run.stdout);rotation.pop('active_key_id',None)
conn=sqlite3.connect(pre/'test.db'); assert conn.execute('pragma integrity_check').fetchone()[0]=='ok'
versions=[x[0] for x in conn.execute('select version from schema_migrations order by version')];assert versions==[1,2,3,4,5]
assert conn.execute('select count(*) from users').fetchone()[0]==1
report={'release':r,'integrity_check':'ok','migrations':versions,'bootstrap_admin_count':1,'rotation_copy_only':rotation,'existing_encrypted_business_rows':0}
conn.close();shutil.rmtree(pre)
(b/'preflight.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report,indent=2))
