"""偽Docker/DBだけで承認・失敗・更新順序を検査する。実サービスには接続しない。"""
import json
import os
import pathlib
import pty
import shutil
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).parent
MOCK = r'''#!/usr/bin/python3
import json, os, pathlib, sys
root = pathlib.Path(os.environ['MOCK_ROOT'])
tool, args = pathlib.Path(sys.argv[0]).name, sys.argv[1:]
with (root/'calls').open('a') as f:
    f.write(json.dumps([tool, *args])+'\n')
failure = os.environ.get('MOCK_FAILURE', '')
state = root/'schema'
assets = ('MISSKEY_STATIC_DIR', 'MISSKEY_REPO_ASSETS_DIR', 'MISSKEY_TWEMOJI_DIR',
          'MISSKEY_FLUENT_EMOJI_DIR', 'MISSKEY_FRONTEND_DIR',
          'MISSKEY_FRONTEND_DIST_DIR', 'MISSKEY_CLIENT_ASSETS_DIR')
old_env = [k+'=/old/'+k for k in assets]
new_env = [k+'=/new/'+k for k in assets]
if tool == 'psql':
    print('100' if 'pg_database_size' in args[-1] else ('1,2,3' if failure == 'hsr-schema' else '1,2,3,4') if 'plugin_hsr.schema_migrations' in args[-1] else state.read_text())
elif tool == 'pg_dump':
    path = pathlib.Path(next(a.split('=',1)[1] for a in args if a.startswith('--file=')))
    if failure == 'before' and path.name == 'database-before': sys.exit(1)
    if failure == 'final' and path.name == 'database-final': sys.exit(1)
    path.mkdir(); (path/'toc.dat').write_text('mock snapshot')
elif tool == 'pg_restore':
    print('mock backup list')
elif tool == 'docker':
    cmd = args[0]
    if cmd == 'inspect':
        if '--format' in args:
            fmt = args[args.index('--format')+1]
            print('old-image' if fmt == '{{.Image}}' else 'old-id' if fmt == '{{.Id}}' else 'true/0')
        else:
            d = {'Id':'old-id','Image':'old-image','RestartCount':0,
                 'State':{'Running':True,'StartedAt':'fixed'},
                 'Config':{'User':'1001:1001','WorkingDir':'/app','Entrypoint':['/app/misskey'],
                           'Cmd':['-config','.config/default.yml'],'Env':old_env},
                 'HostConfig':{'NetworkMode':'host','RestartPolicy':{'Name':'unless-stopped'}},
                 'Mounts':[{'Type':'bind','Source':str(root/'config.yml'),
                             'Destination':'/app/.config/default.yml','RW':False}]}
            if os.environ.get('MOCK_SOURCE', '109') == '117':
                d['Config']['Entrypoint'] = ['/app/elythia']
                d['Config']['Cmd'] = ['serve','-config','/app/.config/default.yml']
            print(json.dumps([d]))
    elif cmd == 'image':
        if '--format' in args: print('linux/amd64')
        else: print(json.dumps([{'Config':{'Env':old_env if args[-1]=='old-image' else new_env}}]))
    elif cmd == 'run' and 'migrate' in args:
        if failure == 'migration':
            state.write_text('110|true'); sys.exit(1)
        state.write_text('117|false')
    elif cmd == 'create': print('new-id')
    elif cmd == 'logs':
        for name in ('role-level','genshin','fedwatch','hsr'):
            print('msg="plugin loaded" name='+name+(' version=0.2.0 migrations=4' if name=='hsr' and failure!='hsr-old' else ''))
    elif cmd == 'export':
        if failure == 'export': sys.exit(1)
        print('mock container archive')
    elif cmd == 'exec':
        if failure == 'health' and 'healthcheck' in args: sys.exit(1)
        if failure == 'doctor' and 'doctor' in args: sys.exit(1)
'''


@unittest.skipUnless(os.geteuid() == 0, 'CIでrootの偽コマンド環境を使う')
class UpdateFlowTests(unittest.TestCase):
    def run_flow(self, *, check=False, approve=True, failure='', source='109'):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            bins = root / 'bin'
            bins.mkdir()
            for name in ('docker', 'psql', 'pg_dump', 'pg_restore'):
                path = bins / name
                path.write_text(MOCK)
                path.chmod(0o700)
            (root / 'schema').write_text(source+'|false')
            (root / 'config.yml').write_text('db: {host: localhost, port: 5432, db: mk1, user: misskey, pass: mock-private-password}')
            shutil.copyfile(ROOT / 'update-misaki-helper.py', root / 'update-misaki-helper.py')
            script_text = (ROOT / 'update-misaki.sh').read_text()
            script_text = script_text.replace("CONFIG='/home/misskey/cherrypick/.config/default.yml'", 'CONFIG=' + repr(str(root / 'config.yml')))
            script_text = script_text.replace("BACKUP_ROOT='/home/misaki/mk-update-backups'", 'BACKUP_ROOT=' + repr(str(root / 'backups')))
            script_text = script_text.replace('HEALTH_TIMEOUT=180', 'HEALTH_TIMEOUT=1')
            script = root / 'update-misaki.sh'
            script.write_text(script_text)
            environment = dict(os.environ, PATH=str(bins) + ':/usr/bin:/bin', MOCK_ROOT=str(root), MOCK_FAILURE=failure, MOCK_SOURCE=source)
            if check:
                result = subprocess.run(['/bin/bash', str(script), '--check'], env=environment, capture_output=True, timeout=30)
            else:
                master, slave = pty.openpty()
                try:
                    process = subprocess.Popen(['/bin/bash', str(script)], stdin=slave, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=environment)
                    os.write(master, (('UPDATE mk-go-production' if approve else 'CANCEL') + '\n').encode())
                    stdout, stderr = process.communicate(timeout=30)
                    result = subprocess.CompletedProcess(process.args, process.returncode, stdout, stderr)
                finally:
                    os.close(master)
                    os.close(slave)
            calls = [json.loads(line) for line in (root / 'calls').read_text().splitlines()]
            logs = '\n'.join(p.read_text() for p in (root / 'backups').glob('*/update.log'))
            self.assertNotIn('mock-private-password', result.stdout.decode() + result.stderr.decode() + logs + json.dumps(calls))
            self.assertFalse(list((root / 'backups').glob('.preflight-*')), '一時pgpass/inspectを残してはいけない')
            return result.returncode, calls

    def test_check_does_not_pull_or_mutate(self):
        rc, calls = self.run_flow(check=True)
        self.assertEqual(rc, 0)
        self.assertFalse(any(c[:2] in (['docker', 'pull'], ['docker', 'stop'], ['docker', 'update'], ['docker', 'run']) for c in calls))

    def test_cancel_leaves_production_running(self):
        rc, calls = self.run_flow(approve=False)
        self.assertNotEqual(rc, 0)
        self.assertFalse(any(c[:2] in (['docker', 'pull'], ['docker', 'stop'], ['docker', 'update']) for c in calls))

    def test_elythia117_check_and_update_without_main_migration(self):
        rc, calls = self.run_flow(check=True, source='117')
        self.assertEqual(rc, 0)
        self.assertFalse(any(c[:2] in (['docker', 'pull'], ['docker', 'stop'], ['docker', 'update'], ['docker', 'run']) for c in calls))
        rc, calls = self.run_flow(source='117')
        self.assertEqual(rc, 0)
        self.assertFalse(any(c[:2] == ['docker', 'run'] and 'migrate' in c for c in calls))
        self.assertIn(['docker', 'start', 'new-id'], calls)

    def test_wrong_source_schema_fails_before_pull(self):
        rc, calls = self.run_flow(source='110')
        self.assertNotEqual(rc, 0)
        self.assertFalse(any(c[:2] in (['docker', 'pull'], ['docker', 'stop'], ['docker', 'update']) for c in calls))

    def test_old_hsr_or_missing_plugin_migration_stops_new(self):
        for failure in ('hsr-old', 'hsr-schema'):
            with self.subTest(failure=failure):
                rc, calls = self.run_flow(source='117', failure=failure)
                self.assertNotEqual(rc, 0)
                self.assertIn(['docker', 'stop', 'new-id'], calls)
                self.assertNotIn(['docker', 'start', 'old-id'], calls)

    def test_approval_runs_to_healthy_start_without_more_input(self):
        rc, calls = self.run_flow()
        self.assertEqual(rc, 0)
        migration = next(i for i, c in enumerate(calls) if c[:2] == ['docker', 'run'] and 'migrate' in c)
        stopped = calls.index(['docker', 'stop', 'old-id'])
        started = calls.index(['docker', 'start', 'new-id'])
        final = next(i for i, c in enumerate(calls) if c[0] == 'pg_dump' and any('database-final' in v for v in c))
        self.assertLess(migration, stopped)
        self.assertLess(stopped, final)
        self.assertLess(final, started)
        self.assertGreater(calls.index(['docker', 'export', 'old-id']), started)
        self.assertTrue(any(c[:3] == ['docker', 'exec', 'new-id'] and 'doctor' in c for c in calls))

    def test_backup_failure_before_migration_does_not_stop_old(self):
        rc, calls = self.run_flow(failure='before')
        self.assertNotEqual(rc, 0)
        self.assertFalse(any(c[:2] in (['docker', 'stop'], ['docker', 'update']) for c in calls))

    def test_migration_or_final_backup_failure_never_restarts_old(self):
        for failure in ('migration', 'final'):
            with self.subTest(failure=failure):
                rc, calls = self.run_flow(failure=failure)
                self.assertNotEqual(rc, 0)
                self.assertIn(['docker', 'stop', 'old-id'], calls)
                self.assertFalse(any(c[:2] == ['docker', 'start'] for c in calls))

    def test_health_failure_stops_new_and_leaves_old_stopped(self):
        rc, calls = self.run_flow(failure='health')
        self.assertNotEqual(rc, 0)
        self.assertIn(['docker', 'stop', 'new-id'], calls)
        self.assertNotIn(['docker', 'start', 'old-id'], calls)

    def test_post_start_failure_does_not_stop_healthy_server(self):
        for failure in ('export', 'doctor'):
            with self.subTest(failure=failure):
                rc, calls = self.run_flow(failure=failure)
                self.assertNotEqual(rc, 0)
                self.assertIn(['docker', 'start', 'new-id'], calls)
                self.assertNotIn(['docker', 'stop', 'new-id'], calls)


if __name__ == '__main__':
    unittest.main()
