#!/usr/bin/env python3
"""Read existing MinIO music; never print credentials or signed URLs."""
import argparse
import concurrent.futures
import json
from datetime import datetime, timezone
import socket
import ssl
import subprocess
import tempfile
import time
import tomllib
import urllib.error
import urllib.request
from pathlib import Path

import boto3
from botocore.config import Config

p = argparse.ArgumentParser()
p.add_argument('--config', required=True, help='existing private TOML, read only')
p.add_argument('--public-origin', required=True)
p.add_argument('--internal-origin', default='http://127.0.0.1:19000')
p.add_argument('--output', default='evidence/owned-audio-probe.json')
p.add_argument('--scan-limit', type=int, default=30)
p.add_argument('--track-id', action='append', default=[], help='existing numerical song ID; repeat for a two-track check')
p.add_argument('--ffprobe', help='optional ffprobe executable; inspect downloaded audio on this server, not a phone')
args = p.parse_args()
c = tomllib.loads(Path(args.config).read_text())['minio_config']
client_options = dict(aws_access_key_id=c['ak'], aws_secret_access_key=c['sk'], region_name='us-east-1', config=Config(signature_version='s3v4', s3={'addressing_style': 'path'}, proxies={}, connect_timeout=5, read_timeout=10, retries={'max_attempts': 0}))
internal = boto3.client('s3', endpoint_url=args.internal_origin, **client_options)
external = boto3.client('s3', endpoint_url=args.public_origin, **client_options)
bucket = 'cloudmusic'
if args.track_id:
    assert all(x.isdigit() and len(x) <= 20 for x in args.track_id), 'numerical track IDs only'
    audio = {'music/' + x + '.mp3': internal.head_object(Bucket=bucket, Key='music/' + x + '.mp3')['ContentLength'] for x in args.track_id}
    infos = [{'Key': 'info/' + x + '.json'} for x in args.track_id]
else:
    audio = {x['Key']: x['Size'] for x in internal.list_objects_v2(Bucket=bucket, Prefix='music/', MaxKeys=args.scan_limit).get('Contents', [])}
    infos = internal.list_objects_v2(Bucket=bucket, Prefix='info/', MaxKeys=args.scan_limit).get('Contents', [])

def metadata(item):
    try:
        key = item['Key']
        track_id = key.split('/')[-1].split('.')[0]
        matches = [k for k in audio if k.startswith('music/' + track_id + '.')]
        if not matches:
            return None
        d = json.loads(internal.get_object(Bucket=bucket, Key=key)['Body'].read(262144))
        duration = float(d.get('Duration', 0))
        # The existing descriptor stores milliseconds. No stored playback URL retained.
        artists = d.get('Artist', '')
        if isinstance(artists, list):
            artists = ', '.join(str(a.get('name', '')) for a in artists if isinstance(a, dict))
        return {'id': 'netease:' + track_id, 'objectKey': matches[0], 'title': str(d.get('Title', ''))[:100], 'durationMs': duration, 'artist': str(artists)[:100], 'bytes': audio[matches[0]]}
    except Exception:
        return None

with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
    candidates = [d for d in pool.map(metadata, infos) if d]
assert len(candidates) >= 2, 'two existing audio objects required'
mp3 = [d for d in candidates if d['objectKey'].endswith('.mp3')]
assert len(mp3) >= 2, 'two existing MP3 objects required for mobile tests'
selected = mp3[:2] if args.track_id else sorted(mp3, key=lambda d: d['durationMs'], reverse=True)[:2]

opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
def request(url, headers=None):
    try:
        with opener.open(urllib.request.Request(url, headers=headers or {}), timeout=20) as r:
            data = r.read(64)
            return {'http': r.status, 'contentType': r.headers.get('Content-Type'), 'contentRange': r.headers.get('Content-Range'), 'contentLength': r.headers.get('Content-Length'), 'receivedBytes': len(data)}
    except urllib.error.HTTPError as e:
        return {'http': e.code}

from urllib.parse import urlsplit
origin = urlsplit(args.public_origin)
with socket.create_connection((origin.hostname, origin.port or 443), timeout=10) as sock:
    with ssl.create_default_context().wrap_socket(sock, server_hostname=origin.hostname) as conn:
        cert = conn.getpeercert()
        tls = {'verified': True, 'version': conn.version(), 'notAfter': cert.get('notAfter')}

rows = []
for item in selected:
    url = external.generate_presigned_url('get_object', Params={'Bucket': bucket, 'Key': item['objectKey']}, ExpiresIn=300)
    result = request(url, {'Range': 'bytes=0-63'})
    assert result['http'] == 206, 'Range GET failed: ' + str(result['http'])
    row = dict(item, network=result)
    if args.ffprobe:
        with tempfile.TemporaryDirectory(prefix='betago-audio-probe-') as temp:
            local = Path(temp) / 'audio.mp3'
            with opener.open(url, timeout=30) as source, local.open('wb') as target:
                while chunk := source.read(262144):
                    target.write(chunk)
            assert local.stat().st_size == item['bytes'], 'downloaded object length mismatch'
            checked = subprocess.run([args.ffprobe, '-v', 'error', '-show_entries', 'format=duration,format_name,size:stream=codec_name,sample_rate,channels', '-of', 'json', str(local)], capture_output=True, text=True, timeout=30)
            assert checked.returncode == 0, 'server audio format inspection failed'
            row['serverAudioInspection'] = json.loads(checked.stdout)
    rows.append(row)

key = selected[0]['objectKey']
short = external.generate_presigned_url('get_object', Params={'Bucket': bucket, 'Key': key}, ExpiresIn=2)
before = request(short, {'Range': 'bytes=0-63'})
time.sleep(4)
expired = request(short, {'Range': 'bytes=0-63'})
fresh = external.generate_presigned_url('get_object', Params={'Bucket': bucket, 'Key': key}, ExpiresIn=300)
renewed = request(fresh, {'Range': 'bytes=0-63'})
assert before['http'] == 206 and expired['http'] == 403 and renewed['http'] == 206, 'expected 206 -> 403 -> 206'
out = {'observedAt': datetime.now(timezone.utc).isoformat(), 'probeVantage': 'server using public DNS and TLS; independent external device still pending', 'mode': 'EXISTING_MINIO_AUDIO', 'nativeFeishuDeviceProof': False, 'publicOrigin': args.public_origin, 'tls': tls, 'audioObjectsInspected': len(audio), 'metadataMatches': len(candidates), 'tracks': rows, 'expiry': {'before': before['http'], 'afterExpiry': expired['http'], 'renewed': renewed['http']}, 'credentialsLogged': False, 'signedUrlsLogged': False}
Path(args.output).parent.mkdir(parents=True, exist_ok=True)
Path(args.output).write_text(json.dumps(out, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(out, ensure_ascii=False, indent=2))
