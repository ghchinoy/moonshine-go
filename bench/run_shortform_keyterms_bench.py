#!/usr/bin/env python3
"""
Benchmark: Keyterm Biasing Comparison on Short-Form LibriSpeech (12 Samples, 110.9s Audio).

Evaluates tiny-streaming and medium-streaming with and without:
  --keyterms "Kirtland,Philip,Munchkins,Gillikins,brethren"

Dataset: /Users/ghchinoy/projects/omi-dev/benchmarks/data/stt_benchmark_audio_02/manifest.json
Output:  /Users/ghchinoy/projects/omi-dev/benchmarks/results/moonshine_go_shortform_keyterms.json
"""

import json
import re
import subprocess
import time
from pathlib import Path
import jiwer
from tabulate import tabulate

REPO_ROOT = Path("/Users/ghchinoy/projects/moonshine-go")
BINARY = REPO_ROOT / "bin" / "moonshine"
MANIFEST_PATH = Path("/Users/ghchinoy/projects/omi-dev/benchmarks/data/stt_benchmark_audio_02/manifest.json")
AUDIO_DIR = MANIFEST_PATH.parent
OUT_PATH = Path("/Users/ghchinoy/projects/omi-dev/benchmarks/results/moonshine_go_shortform_keyterms.json")

KEYTERMS = "Kirtland,Philip,Munchkins,Gillikins,brethren"
PUNCT_RE = re.compile(r'[^\w\s]', re.UNICODE)


def normalize_for_wer(text: str) -> str:
    cleaned = text.replace(" '", "'")
    cleaned = PUNCT_RE.sub('', cleaned).lower().strip()
    return re.sub(r'\s+', ' ', cleaned)


def run_transcribe(arch: str, wav_path: Path, keyterms: str = None) -> dict:
    cmd = [
        str(BINARY),
        "transcribe",
        "--json",
        "--arch", arch,
    ]
    if keyterms:
        cmd.extend(["--keyterms", keyterms])
    cmd.append(str(wav_path))

    t0 = time.monotonic()
    proc = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, check=True)
    latency_s = time.monotonic() - t0
    data = json.loads(proc.stdout)
    return {
        "data": data,
        "latency_s": latency_s,
    }


def main():
    with open(MANIFEST_PATH, "r") as f:
        manifest = json.load(f)

    models = ["tiny-streaming", "medium-streaming"]
    conditions = ["baseline", "with_keyterms"]

    print("=" * 80)
    print(" MOONSHINE-GO: SHORT-FORM KEYTERMS BENCHMARK (12 Samples, 110.9s)")
    print(f" Keyterms: {KEYTERMS}")
    print(f" Models:   {', '.join(models)}")
    print("=" * 80)

    report = {
        "benchmark": "librispeech_shortform_keyterms",
        "date": time.strftime("%Y-%m-%d"),
        "keyterms": KEYTERMS,
        "total_samples": len(manifest),
        "total_audio_s": sum(item["duration_s"] for item in manifest),
        "results": {},
    }

    for arch in models:
        report["results"][arch] = {}
        for cond in conditions:
            use_kt = KEYTERMS if cond == "with_keyterms" else None
            cond_label = f"[{arch} - {cond}]"
            print(f"\n---> Evaluating {cond_label}...")

            samples_res = []
            tot_audio = 0.0
            tot_infer = 0.0
            tot_ref_words = 0
            tot_subs = 0
            tot_dels = 0
            tot_ins = 0

            for item in manifest:
                sid = item["id"]
                wav_path = AUDIO_DIR / f"{sid}.wav"
                dur_s = item["duration_s"]
                ref_raw = item["text"]

                run_res = run_transcribe(arch, wav_path, use_kt)
                res_data = run_res["data"]
                stats = res_data.get("stats", {})
                lines = res_data.get("lines", [])
                hyp_raw = " ".join([l.get("text", "") for l in lines]).strip()

                ref_norm = normalize_for_wer(ref_raw)
                hyp_norm = normalize_for_wer(hyp_raw)

                wer_res = jiwer.process_words(ref_norm, hyp_norm)
                sample_wer = wer_res.wer * 100.0

                infer_ms = stats.get("inference_ms", 0.0)
                infer_s = infer_ms / 1000.0
                rtf = dur_s / infer_s if infer_s > 0 else 0.0

                tot_audio += dur_s
                tot_infer += infer_s
                ref_words = len(ref_norm.split())
                hyp_words = len(hyp_norm.split())
                tot_ref_words += ref_words
                tot_subs += wer_res.substitutions
                tot_dels += wer_res.deletions
                tot_ins += wer_res.insertions

                print(f"  [{sid}] ({dur_s:.1f}s): WER={sample_wer:.1f}% (S={wer_res.substitutions}, D={wer_res.deletions}, I={wer_res.insertions}) | hyp: {hyp_raw[:60]}...")

                samples_res.append({
                    "id": sid,
                    "duration_s": dur_s,
                    "ref_words": ref_words,
                    "hyp_words": hyp_words,
                    "wer_pct": round(sample_wer, 2),
                    "substitutions": wer_res.substitutions,
                    "deletions": wer_res.deletions,
                    "insertions": wer_res.insertions,
                    "inference_ms": round(infer_ms, 1),
                    "speed_x": round(rtf, 2),
                    "hyp_text": hyp_raw,
                })

            corpus_edits = tot_subs + tot_dels + tot_ins
            corpus_wer = (corpus_edits / tot_ref_words) * 100.0 if tot_ref_words > 0 else 0.0
            agg_rtf = tot_audio / tot_infer if tot_infer > 0 else 0.0

            report["results"][arch][cond] = {
                "corpus_wer_pct": round(corpus_wer, 2),
                "aggregate_speed_x": round(agg_rtf, 2),
                "total_inference_ms": round(tot_infer * 1000.0, 1),
                "total_substitutions": tot_subs,
                "total_deletions": tot_dels,
                "total_insertions": tot_ins,
                "total_ref_words": tot_ref_words,
                "samples": samples_res,
            }

            print(f"  ==> {cond_label} Corpus WER: {corpus_wer:.2f}% (Edits: S={tot_subs}, D={tot_dels}, I={tot_ins} / {tot_ref_words} words) | Speed: {agg_rtf:.1f}x RT")

    OUT_PATH.parent.mkdir(parents=True, exist_ok=True)
    with open(OUT_PATH, "w") as f:
        json.dump(report, f, indent=2)

    print(f"\n✓ Saved full structured results to {OUT_PATH}")

    # Summary table
    headers = [
        "Sample ID", "Target Keyterm", "Ref Words",
        "Tiny Base WER", "Tiny +KT WER",
        "Med Base WER", "Med +KT WER"
    ]
    target_map = {
        "sample_01": "Lake",
        "sample_02": "-",
        "sample_03": "brethren",
        "sample_04": "-",
        "sample_05": "-",
        "sample_06": "-",
        "sample_07": "Holmes",
        "sample_08": "Philip",
        "sample_09": "Munchkins, Gillikins",
        "sample_10": "Kirtland",
        "sample_11": "-",
        "sample_12": "-",
    }

    rows = []
    for i, item in enumerate(manifest):
        sid = item["id"]
        kt = target_map.get(sid, "-")
        rw = item["word_count"]
        tb = f"{report['results']['tiny-streaming']['baseline']['samples'][i]['wer_pct']:.1f}%"
        tkt = f"{report['results']['tiny-streaming']['with_keyterms']['samples'][i]['wer_pct']:.1f}%"
        mb = f"{report['results']['medium-streaming']['baseline']['samples'][i]['wer_pct']:.1f}%"
        mkt = f"{report['results']['medium-streaming']['with_keyterms']['samples'][i]['wer_pct']:.1f}%"
        rows.append([sid, kt, rw, tb, tkt, mb, mkt])

    rows.append([
        "CORPUS TOTAL",
        "5 Keyterms",
        report['results']['tiny-streaming']['baseline']['total_ref_words'],
        f"{report['results']['tiny-streaming']['baseline']['corpus_wer_pct']:.2f}%",
        f"{report['results']['tiny-streaming']['with_keyterms']['corpus_wer_pct']:.2f}%",
        f"{report['results']['medium-streaming']['baseline']['corpus_wer_pct']:.2f}%",
        f"{report['results']['medium-streaming']['with_keyterms']['corpus_wer_pct']:.2f}%",
    ])

    print("\n" + "=" * 90)
    print(tabulate(rows, headers=headers, tablefmt="github"))
    print("=" * 90)


if __name__ == "__main__":
    main()
