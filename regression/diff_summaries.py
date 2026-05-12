#!/usr/bin/env python3
"""
Diff fresh WAV renders against committed reference summaries with tolerance.

The Java engine has phase jitter across runs, so we don't compare samples.
Instead we tolerate small drift in: total frame count (startup jitter),
peak (rare oscillator collisions), RMS, per-window RMS envelope, and the
zero-frame percentage.

Exit 0 if every fixture passes, non-zero otherwise.
"""
import argparse, json, sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from summarize import summarize

# Tolerances tuned to the Java engine's inherent inter-run jitter:
# tail-duration depends on GrimReaper scheduling, polyphonic peaks vary
# with phase interference, channel-context LFOs (PWM sweep) start at
# different phases each run. The thresholds catch gross regressions
# (silence, wrong patch, stuck notes, doubled amplitude, gross shape
# changes) but not subtle DSP changes - that's by design, since the
# engine can't produce a tighter reference than this.
FRAMES_ABS = 25000        # ~0.57s
PEAK_REL = 0.50
PEAK_ABS_FLOOR = 200
RMS_REL = 0.35
RMS_ABS_FLOOR = 80
ENV_REL = 0.50
ENV_ABS_FLOOR = 150
ENV_BAD_FRACTION = 0.40
ENV_LEN_DIFF = 5
ZERO_PP = 5.0

def scalar_diff(label, ref, fresh, rel, abs_floor):
    if ref == 0 and fresh == 0:
        return None
    diff = abs(ref - fresh)
    tol = max(rel * max(abs(ref), abs(fresh)), abs_floor)
    if diff > tol:
        return f"{label}: ref={ref} fresh={fresh} |diff|={diff:.2f} > tol={tol:.2f}"
    return None

def diff_summary(ref, fresh):
    errs = []

    df = abs(ref["frames"] - fresh["frames"])
    if df > FRAMES_ABS:
        errs.append(f"frames: ref={ref['frames']} fresh={fresh['frames']} diff={df} > {FRAMES_ABS}")

    if e := scalar_diff("peak", ref["peak"], fresh["peak"], PEAK_REL, PEAK_ABS_FLOOR):
        errs.append(e)
    if e := scalar_diff("rms", ref["rms"], fresh["rms"], RMS_REL, RMS_ABS_FLOOR):
        errs.append(e)

    if abs(ref["zero_frame_pct"] - fresh["zero_frame_pct"]) > ZERO_PP:
        errs.append(
            f"zero_frame_pct: ref={ref['zero_frame_pct']} fresh={fresh['zero_frame_pct']}"
        )

    ra, rb = ref["rms_envelope"], fresh["rms_envelope"]
    if abs(len(ra) - len(rb)) > ENV_LEN_DIFF:
        errs.append(f"rms_envelope length: ref={len(ra)} fresh={len(rb)}")
    else:
        n = min(len(ra), len(rb))
        bad = []
        for i in range(n):
            if scalar_diff(f"env[{i}]", ra[i], rb[i], ENV_REL, ENV_ABS_FLOOR):
                bad.append(i)
        if len(bad) > max(2, int(ENV_BAD_FRACTION * n)):
            errs.append(
                f"rms_envelope: {len(bad)}/{n} buckets out of tolerance "
                f"(first few: {bad[:5]})"
            )

    return errs

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--ref", required=True, help="committed summary dir")
    ap.add_argument("--wav", required=True, help="fresh wav dir to compare")
    args = ap.parse_args()
    ref_dir = Path(args.ref)
    wav_dir = Path(args.wav)

    refs = sorted(ref_dir.glob("*.json"))
    n_pass = n_fail = n_miss = 0

    fresh_names = {p.stem for p in wav_dir.glob("*.wav")}
    ref_names = {p.stem for p in refs}
    extra = fresh_names - ref_names
    if extra:
        print(f"WARN  unexpected WAVs not covered by committed summaries: {sorted(extra)}")

    for ref_path in refs:
        name = ref_path.stem
        wav_path = wav_dir / f"{name}.wav"
        ref = json.loads(ref_path.read_text())
        if not wav_path.exists():
            print(f"MISS  {name}: no {wav_path.name}")
            n_miss += 1
            continue
        fresh = summarize(wav_path)
        errs = diff_summary(ref, fresh)
        if errs:
            print(f"FAIL  {name}")
            for e in errs:
                print(f"        {e}")
            n_fail += 1
        else:
            print(f"PASS  {name}")
            n_pass += 1

    total = n_pass + n_fail + n_miss
    print()
    print(f"{n_pass}/{total} passed  ({n_fail} failed, {n_miss} missing)")
    sys.exit(0 if (n_fail == 0 and n_miss == 0) else 1)

if __name__ == "__main__":
    main()
