#!/usr/bin/env python3
"""単独本番containerの構成検査。認証情報はstdoutへ出さない。"""
import json
import os
import re
import subprocess
import sys

ASSET_KEYS = (
    'MISSKEY_STATIC_DIR', 'MISSKEY_REPO_ASSETS_DIR', 'MISSKEY_TWEMOJI_DIR',
    'MISSKEY_FLUENT_EMOJI_DIR', 'MISSKEY_FRONTEND_DIR',
    'MISSKEY_FRONTEND_DIST_DIR', 'MISSKEY_CLIENT_ASSETS_DIR',
)


def load(path):
    with open(path, encoding='utf-8') as f:
        return json.load(f)[0]


def require(ok, message):
    if not ok:
        raise SystemExit('ERROR: ' + message)


def env(config):
    items = config.get('Env') or []
    require(all('\n' not in v and '\r' not in v and '=' in v for v in items), '不正な環境変数')
    result = dict(v.split('=', 1) for v in items)
    require(len(result) == len(items), '環境変数が重複しています')
    return result


def validate(d, config, image):
    c, h = d['Config'], d['HostConfig']
    require(d['State']['Running'] and not d['State'].get('Paused') and not d['State'].get('Restarting'), '本番containerが正常稼働していません')
    require(h['NetworkMode'] == 'host', 'host networkが必要です')
    require(c['User'] == '1001:1001' and c['WorkingDir'] == '/app', 'ユーザー/作業場所が想定と異なります')
    require(c['Entrypoint'] == ['/app/misskey'] and c['Cmd'] == ['-config', '.config/default.yml'], '1.5.0の起動コマンドではありません')
    require(h['RestartPolicy']['Name'] == 'unless-stopped', 'restart policyが想定と異なります')
    m = d['Mounts']
    require(len(m) == 1 and m[0]['Type'] == 'bind' and m[0]['Source'] == config and m[0]['Destination'] == '/app/.config/default.yml' and not m[0]['RW'], '設定mountが想定と異なります')
    unsupported = ('Privileged', 'CapAdd', 'CapDrop', 'SecurityOpt', 'Devices', 'DeviceRequests',
                   'VolumesFrom', 'Tmpfs', 'ExtraHosts', 'Dns', 'DnsSearch', 'DnsOptions',
                   'Links', 'Ulimits', 'GroupAdd', 'Sysctls', 'CpusetCpus', 'CpusetMems',
                   'BlkioDeviceReadBps', 'BlkioDeviceWriteBps', 'BlkioDeviceReadIOps',
                   'BlkioDeviceWriteIOps', 'DeviceCgroupRules', 'CgroupParent', 'AutoRemove',
                   'PublishAllPorts', 'PortBindings', 'ReadonlyRootfs', 'OomKillDisable',
                   'PidMode', 'UTSMode', 'UsernsMode', 'CpuShares', 'CpuPeriod', 'CpuQuota',
                   'NanoCpus', 'Memory', 'MemoryReservation', 'MemorySwap', 'MemorySwappiness',
                   'PidsLimit', 'OomScoreAdj', 'BlkioWeight')
    for key in unsupported:
        require(not h.get(key), '未対応の起動設定: ' + key)
    require(h.get('IpcMode', 'private') == 'private', '独自IPC設定があります')
    require(not c.get('Tty') and not c.get('OpenStdin'), '対話型containerは非対応です')
    health = c.get('Healthcheck') or {}
    require(not health.get('Test') or health['Test'] == ['NONE'], '独自healthcheckがあります')
    e, original = env(c), env(image['Config'])
    for key in ASSET_KEYS:
        require(e.get(key) == original.get(key), '独自assets環境変数は自動変換できません: ' + key)
    for key, accepted in {'MK_DB_HOST': ('localhost', '127.0.0.1'), 'MK_DB_PORT': ('5432',),
                          'MK_DB_DB': ('mk1',), 'MK_DB_USER': ('misskey',)}.items():
        require(key not in e or e[key] in accepted, 'DB環境変数が想定と異なります: ' + key)


def pgpass(d, config, destination, supplied):
    import yaml
    try:
        with open(config, encoding='utf-8') as f:
            settings = yaml.safe_load(f)
    except yaml.YAMLError:
        raise SystemExit('ERROR: 設定YAMLを解析できません（内容は表示しません）') from None
    require(isinstance(settings, dict), '設定YAMLが辞書ではありません')
    db = settings.get('db') or {}
    require(isinstance(db, dict), 'DB設定が辞書ではありません')
    e = env(d['Config'])
    for key, yaml_key, expected in [('MK_DB_HOST', 'host', ('localhost', '127.0.0.1')),
                                    ('MK_DB_PORT', 'port', ('5432',)),
                                    ('MK_DB_DB', 'db', ('mk1',)),
                                    ('MK_DB_USER', 'user', ('misskey',))]:
        require(str(e.get(key, db.get(yaml_key, ''))) in expected, '設定のDB接続先が想定と異なります: ' + key)
    password = supplied or e.get('MK_DB_PASS', db.get('pass', ''))
    require(isinstance(password, str) and bool(password) and '\n' not in password and '\r' not in password, 'DBパスワードが空または不正です')
    escaped = password.replace('\\', '\\\\').replace(':', '\\:')
    with open(destination, 'w', encoding='utf-8') as f:
        os.chmod(destination, 0o600)
        f.write('localhost:5432:mk1:misskey:' + escaped + '\n')


def environment(d, image, destination):
    values, defaults = env(d['Config']), env(image['Config'])
    for key in ASSET_KEYS:
        require(key in defaults, '新imageにassets環境変数がありません: ' + key)
        values[key] = defaults[key]
    with open(destination, 'w', encoding='utf-8') as f:
        for key, value in values.items():
            f.write(key + '=' + value + '\n')


def create(d, env_file, image, name, config):
    c, h = d['Config'], d['HostConfig']
    args = ['docker', 'create', '--name', name, '--network', 'host', '--user', c['User'],
            '--workdir', '/app', '--restart', 'unless-stopped', '--env-file', env_file,
            '--mount', 'type=bind,source=' + config + ',target=/app/.config/default.yml,readonly',
            '--entrypoint', '/app/elythia']
    if h.get('Init'):
        args.append('--init')
    if h.get('ShmSize'):
        args += ['--shm-size', str(h['ShmSize'])]
    if c.get('StopSignal'):
        args += ['--stop-signal', c['StopSignal']]
    if c.get('StopTimeout') is not None:
        args += ['--stop-timeout', str(c['StopTimeout'])]
    log = h.get('LogConfig') or {}
    if log.get('Type'):
        args += ['--log-driver', log['Type']]
    for k, v in (log.get('Config') or {}).items():
        args += ['--log-opt', k + '=' + str(v)]
    for k, v in (c.get('Labels') or {}).items():
        if not k.startswith('org.opencontainers.image.'):
            args += ['--label', k + '=' + str(v)]
    args += [image, 'serve', '-config', '/app/.config/default.yml']
    subprocess.run(args, check=True)


def plugins(log):
    with open(log, encoding='utf-8') as f:
        lines = f.readlines()
    for name in ('role-level', 'genshin', 'fedwatch', 'hsr'):
        found = False
        for line in lines:
            try:
                item = json.loads(line)
            except ValueError:
                item = {}
            if isinstance(item, dict) and item.get('msg') == 'plugin loaded' and item.get('name') == name:
                found = True
            if 'plugin loaded' in line and re.search(r'\bname=' + re.escape(name) + r'(?:\s|$)', line):
                found = True
        require(found, 'plugin loaded未確認: ' + name)


def main(args):
    command, rest = args[0], args[1:]
    if command == 'validate':
        validate(load(rest[0]), rest[1], load(rest[2]))
    elif command == 'pgpass':
        pgpass(load(rest[0]), rest[1], rest[2], sys.stdin.read())
    elif command == 'environment':
        environment(load(rest[0]), load(rest[1]), rest[2])
    elif command == 'create':
        create(load(rest[0]), *rest[1:])
    elif command == 'plugins':
        plugins(rest[0])
    elif command == 'unchanged':
        before, after = load(rest[0]), load(rest[1])
        require(all(before[k] == after[k] for k in ('Id', 'Image', 'Config', 'HostConfig', 'Mounts')), '本番container構成が準備中に変化しました')
        require(after['State']['Running'], '本番containerが準備中に停止しました')
    else:
        raise SystemExit('不明なhelperコマンド')


if __name__ == '__main__':
    main(sys.argv[1:])
