"""本番にはアクセスせず、更新helperの安全条件を検査する。"""
import copy
import importlib.util
import json
import pathlib
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

ROOT = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location('helper', ROOT / 'update-misaki-helper.py')
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)


def fixture():
    return {
        'State': {'Running': True},
        'Mounts': [{'Type': 'bind', 'Source': '/config.yml', 'Destination': '/app/.config/default.yml', 'RW': False}],
        'Config': {'User': '1001:1001', 'WorkingDir': '/app', 'Entrypoint': ['/app/misskey'],
                   'Cmd': ['-config', '.config/default.yml'], 'Env': ['MK_DB_PASS=test-only'],
                   'Labels': {'operator': 'keep', 'org.opencontainers.image.version': 'old'}},
        'HostConfig': {'NetworkMode': 'host', 'RestartPolicy': {'Name': 'unless-stopped'},
                       'LogConfig': {'Type': 'json-file', 'Config': {'max-size': '10m'}},
                       'Init': True, 'ShmSize': 67108864},
    }


class HelperTests(unittest.TestCase):
    def test_supported_config(self):
        d = fixture()
        helper.validate(d, '/config.yml', copy.deepcopy(d))

    def test_source_schema_must_match_approved_command(self):
        d = fixture()
        helper.validate(d, '/config.yml', copy.deepcopy(d), '109|false')
        with self.assertRaises(SystemExit):
            helper.validate(d, '/config.yml', copy.deepcopy(d), '117|false')
        d['Config']['Entrypoint'] = ['/app/elythia']
        d['Config']['Cmd'] = ['serve', '-config', '/app/.config/default.yml']
        helper.validate(d, '/config.yml', copy.deepcopy(d), '117|false')
        for state in ('109|false', '117|true', '110|false'):
            with self.assertRaises(SystemExit):
                helper.validate(d, '/config.yml', copy.deepcopy(d), state)

    def test_unsupported_settings_fail_closed(self):
        for key, value in [('Privileged', True), ('Memory', 1024), ('CapAdd', ['SYS_ADMIN']), ('PortBindings', {'80/tcp': []})]:
            with self.subTest(key=key):
                d = fixture()
                d['HostConfig'][key] = value
                with self.assertRaises(SystemExit):
                    helper.validate(d, '/config.yml', fixture())

    def test_mount_and_command_fail_closed(self):
        d = fixture()
        d['Mounts'][0]['RW'] = True
        with self.assertRaises(SystemExit):
            helper.validate(d, '/config.yml', fixture())
        d = fixture()
        d['Config']['Entrypoint'] = ['/app/elythia']
        with self.assertRaises(SystemExit):
            helper.validate(d, '/config.yml', fixture())

    def test_custom_assets_are_not_silently_overwritten(self):
        d = fixture()
        d['Config']['Env'].append('MISSKEY_FRONTEND_DIR=/custom')
        with self.assertRaises(SystemExit):
            helper.validate(d, '/config.yml', fixture())

    def test_env_newlines_and_duplicates_fail(self):
        for values in [['KEY=a\nb'], ['KEY=a', 'KEY=b']]:
            with self.assertRaises(SystemExit):
                helper.env({'Env': values})

    def test_asset_paths_use_new_image_defaults(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = pathlib.Path(tmp) / 'env'
            image = {'Config': {'Env': [k + '=/new/' + k for k in helper.ASSET_KEYS]}}
            helper.environment(fixture(), image, output)
            result = output.read_text()
            self.assertIn('MK_DB_PASS=test-only\n', result)
            for k in helper.ASSET_KEYS:
                self.assertIn(k + '=/new/' + k + '\n', result)

    def test_pgpass_escapes_and_uses_config_without_prompt(self):
        with tempfile.TemporaryDirectory() as tmp:
            config, output = pathlib.Path(tmp) / 'config', pathlib.Path(tmp) / 'pgpass'
            config.write_text('db:\n  host: localhost\n  port: 5432\n  db: mk1\n  user: misskey\n  pass: yaml-secret\n')
            helper.pgpass(fixture(), config, output, 'test:slash\\')
            self.assertEqual(output.read_text(), 'localhost:5432:mk1:misskey:test\\:slash\\\\\n')
            self.assertEqual(output.stat().st_mode & 0o777, 0o600)
            helper.pgpass(fixture(), config, output, '')
            self.assertIn('test-only', output.read_text())
            d = fixture()
            d['Config']['Env'] = []
            helper.pgpass(d, config, output, '')
            self.assertIn('yaml-secret', output.read_text())

    def test_wrong_database_fails_before_backup(self):
        with tempfile.TemporaryDirectory() as tmp:
            config = pathlib.Path(tmp) / 'config'
            config.write_text('db: {host: localhost, port: 5432, db: another, user: misskey, pass: secret}')
            with self.assertRaises(SystemExit):
                helper.pgpass(fixture(), config, pathlib.Path(tmp) / 'pgpass', '')

    def test_capacity_requires_two_database_snapshots_and_margin(self):
        with patch.object(helper.os, 'statvfs', return_value=SimpleNamespace(f_bavail=1, f_frsize=4096)):
            with self.assertRaises(SystemExit):
                helper.main(['capacity', '/backup', '100'])
        with patch.object(helper.os, 'statvfs', return_value=SimpleNamespace(f_bavail=1024**3, f_frsize=4096)):
            helper.main(['capacity', '/backup', '100'])

    def test_yaml_parse_failure_does_not_expose_password(self):
        with tempfile.TemporaryDirectory() as tmp:
            config = pathlib.Path(tmp) / 'config'
            config.write_text('db: [secret-do-not-print')
            with self.assertRaises(SystemExit) as failure:
                helper.pgpass(fixture(), config, pathlib.Path(tmp) / 'pgpass', '')
            self.assertNotIn('secret-do-not-print', str(failure.exception))

    def test_create_uses_elythia_and_preserves_operator_settings(self):
        with patch.object(helper.subprocess, 'run') as run:
            helper.create(fixture(), '/env', 'image@sha256:fixed', 'mk-go-production', '/config.yml')
        args = run.call_args.args[0]
        self.assertEqual(args[-4:], ['image@sha256:fixed', 'serve', '-config', '/app/.config/default.yml'])
        self.assertIn('/app/elythia', args)
        self.assertIn('operator=keep', args)
        self.assertNotIn('org.opencontainers.image.version=old', args)
        self.assertIn('max-size=10m', args)
        self.assertIn('--init', args)

    def test_plugins_require_all_four_and_exact_names(self):
        with tempfile.TemporaryDirectory() as tmp:
            log = pathlib.Path(tmp) / 'log'
            log.write_text('\n'.join('msg="plugin loaded" name=' + n + (' version=0.2.0 migrations=4' if n == 'hsr' else '') for n in ('role-level', 'genshin', 'fedwatch', 'hsr')))
            helper.plugins(log)
            log.write_text(log.read_text().replace('name=hsr', 'name=hsr-unrelated'))
            with self.assertRaises(SystemExit):
                helper.plugins(log)
            log.write_text('\n'.join(json.dumps({'msg': 'plugin loaded', 'name': n, 'version': '0.2.0', 'migrations': 4}) for n in ('role-level', 'genshin', 'fedwatch', 'hsr')))
            helper.plugins(log)
            log.write_text(log.read_text().replace('0.2.0', '0.1.0'))
            with self.assertRaises(SystemExit):
                helper.plugins(log)

    def test_shell_flow_keeps_preparation_outside_downtime(self):
        source = (ROOT / 'update-misaki.sh').read_text()
        self.assertEqual(source.count('read -r -p'), 1)
        self.assertLess(source.index('docker pull'), source.index("stage='stopping'"))
        self.assertLess(source.index("stage='online-migration'"), source.index("stage='stopping'"))
        self.assertLess(source.index('database-final"'), source.index('docker start'))
        self.assertGreater(source.index('docker export'), source.index('healthy=1'))
        self.assertNotIn('--password', source)
        self.assertNotIn('/app/migrate', source)


if __name__ == '__main__':
    unittest.main()
