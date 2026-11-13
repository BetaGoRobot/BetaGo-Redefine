#!/usr/bin/env python3
"""Deterministic self-authored CC0 PCM tones; no third party recordings."""
import hashlib
import json
import struct
import wave
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'fixtures' / 'audio'
OUT.mkdir(parents=True, exist_ok=True)
manifest = {'label': 'SYNTHETIC_TEST_FIXTURES_NOT_REAL_DEVICE_PROOF', 'license': 'CC0-1.0', 'sampleRate': 8000, 'tracks': []}
for name, period in [('tone-a', 40), ('tone-b', 32)]:
    # Low-volume triangle wave: integer arithmetic, identical output on all hosts.
    pcm = b''.join(struct.pack('<h', (min(i % period, period - i % period) * 1600 // (period // 2)) - 800) for i in range(8000))
    path = OUT / (name + '.wav')
    with wave.open(str(path), 'wb') as audio:
        audio.setnchannels(1)
        audio.setsampwidth(2)
        audio.setframerate(8000)
        for second in range(960):
            # One quiet second each minute provides a reproducible audible marker.
            audio.writeframes(pcm if second % 60 else bytes(len(pcm)))
    manifest['tracks'].append({'id': 'fixture:' + name, 'file': path.name, 'durationMs': 960000, 'bytes': path.stat().st_size, 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()})
(OUT / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print(json.dumps(manifest, indent=2))
