#!/usr/bin/env python3
"""
Summarize a WAV into a JSON file that the Go port can diff against.

The Java engine is not bit-reproducible across runs (thread-scheduling
jitter in oscillator phase), so we compare summary stats with tolerance
rather than sample-for-sample.
"""
import json, math, struct, sys, wave
from pathlib import Path

WINDOW_MS = 50

def summarize(wav_path: Path) -> dict:
    with wave.open(str(wav_path), 'rb') as w:
        n = w.getnframes()
        sr = w.getframerate()
        ch = w.getnchannels()
        sw = w.getsampwidth()
        raw = w.readframes(n)
    if sw != 2 or ch != 1:
        raise SystemExit(f"{wav_path}: expected mono 16-bit, got {ch}ch {sw*8}-bit")
    samples = struct.unpack(f'<{n}h', raw)

    peak = max((abs(s) for s in samples), default=0)
    rms_total = math.sqrt(sum(s*s for s in samples)/n) if n else 0.0
    zero = sum(1 for s in samples if s == 0)

    win = max(1, (sr * WINDOW_MS) // 1000)
    rms_envelope = []
    for i in range(0, n, win):
        chunk = samples[i:i+win]
        m = len(chunk)
        r = math.sqrt(sum(s*s for s in chunk)/m) if m else 0.0
        rms_envelope.append(round(r, 1))

    return {
        "wav": wav_path.name,
        "frames": n,
        "sample_rate": sr,
        "channels": ch,
        "duration_sec": round(n/sr, 4),
        "peak": peak,
        "rms": round(rms_total, 2),
        "zero_frame_pct": round(100*zero/n, 2) if n else 0.0,
        "window_ms": WINDOW_MS,
        "rms_envelope": rms_envelope,
    }

if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit("usage: summarize.py <input.wav> <output.json>")
    src = Path(sys.argv[1])
    dst = Path(sys.argv[2])
    s = summarize(src)
    dst.write_text(json.dumps(s, indent=2) + "\n")
    print(f"{dst.name}: frames={s['frames']} peak={s['peak']} rms={s['rms']}")
