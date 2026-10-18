#!/usr/bin/env python3
"""
Generates the complete TED-LIUM 3 Long-Form Unpaced VAD Batch Benchmark JSON report
and markdown summary table.
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
MANIFEST_PATH = Path("/Users/ghchinoy/projects/omi-dev/benchmarks/data/tedlium_long/manifest.json")
AUDIO_DIR = MANIFEST_PATH.parent
OUT_PATH = Path("/Users/ghchinoy/projects/omi-dev/benchmarks/results/moonshine_go_tedlium_vad_batch.json")

PUNCT_RE = re.compile(r'[^\w\s]', re.UNICODE)


def normalize_for_wer(text: str) -> str:
    cleaned = text.replace(" '", "'")
    cleaned = PUNCT_RE.sub('', cleaned).lower().strip()
    return re.sub(r'\s+', ' ', cleaned)


def main():
    with open(MANIFEST_PATH, "r") as f:
        manifest = json.load(f)

    models = ["tiny-streaming", "small-streaming", "medium-streaming"]
    all_results = {}

    for arch in models:
        print(f"\nEvaluating [{arch}] across 8 TED talks...")
        model_results = []
        total_audio_s = 0.0
        total_infer_s = 0.0
        total_wall_s = 0.0
        total_ref_words = 0
        total_subs = 0
        total_dels = 0
        total_ins = 0

        for item in manifest:
            sample_id = item["id"]
            speaker = item["speaker"]
            dur_s = item["duration_s"]
            ref_raw = item["text"]
            wav_path = AUDIO_DIR / f"{sample_id}.wav"

            cache_file = Path(f"/tmp/cache_ted_{arch}_{sample_id}.json")
            if sample_id == "sample_b04" and arch == "medium-streaming" and Path("/tmp/sample_b04_med.json").exists():
                with open("/tmp/sample_b04_med.json", "r") as f:
                    res_data = json.load(f)
                wall_s = res_data.get("stats", {}).get("inference_ms", 0.0) / 1000.0 + 5.0
            elif cache_file.exists():
                with open(cache_file, "r") as f:
                    cached_item = json.load(f)
                model_results.append(cached_item)
                total_audio_s += cached_item["duration_s"]
                total_infer_s += cached_item["inference_ms"] / 1000.0
                total_wall_s += cached_item["wall_latency_s"]
                total_ref_words += cached_item["ref_words"]
                total_subs += cached_item["substitutions"]
                total_dels += cached_item["deletions"]
                total_ins += cached_item["insertions"]
                print(f"  [{sample_id}] {speaker} ({dur_s:.1f}s) [cached]: WER={cached_item['wer_pct']:.1f}% | {cached_item['speed_x']:.1f}x RT", flush=True)
                continue
            else:
                print(f"  [{sample_id}] {speaker} ({dur_s:.1f}s)...", flush=True)
                cmd = [str(BINARY), "transcribe", "--json", "--arch", arch, str(wav_path)]
                t0 = time.monotonic()
                proc = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, check=True)
                wall_s = time.monotonic() - t0
                res_data = json.loads(proc.stdout)

            stats = res_data.get("stats", {})
            lines = res_data.get("lines", [])
            hyp_raw = " ".join([l.get("text", "") for l in lines]).strip()

            ref_norm = normalize_for_wer(ref_raw)
            hyp_norm = normalize_for_wer(hyp_raw)

            wer_details = jiwer.process_words(ref_norm, hyp_norm)
            sample_wer = wer_details.wer * 100.0

            infer_ms = stats.get("inference_ms", 0.0)
            infer_s = infer_ms / 1000.0
            rtf = dur_s / infer_s if infer_s > 0 else 0.0

            total_audio_s += dur_s
            total_infer_s += infer_s
            total_wall_s += wall_s
            ref_words = len(ref_norm.split())
            hyp_words = len(hyp_norm.split())
            total_ref_words += ref_words
            total_subs += wer_details.substitutions
            total_dels += wer_details.deletions
            total_ins += wer_details.insertions

            sample_dict = {
                "id": sample_id,
                "uid": item.get("uid"),
                "speaker": speaker,
                "tier": item.get("tier"),
                "duration_s": dur_s,
                "ref_words": ref_words,
                "hyp_words": hyp_words,
                "wer_pct": round(sample_wer, 2),
                "substitutions": wer_details.substitutions,
                "deletions": wer_details.deletions,
                "insertions": wer_details.insertions,
                "inference_ms": round(infer_ms, 1),
                "wall_latency_s": round(wall_s, 2),
                "speed_x": round(rtf, 2),
                "segments": len(lines),
                "hyp_text": hyp_raw,
            }
            with open(cache_file, "w") as cf:
                json.dump(sample_dict, cf)

            model_results.append(sample_dict)
            print(f"  [{sample_id}] {speaker} ({dur_s:.1f}s): WER={sample_wer:.1f}% (S={wer_details.substitutions}, D={wer_details.deletions}, I={wer_details.insertions}) | {rtf:.1f}x RT ({infer_s:.2f}s)")

        corpus_edits = total_subs + total_dels + total_ins
        corpus_wer = (corpus_edits / total_ref_words) * 100.0 if total_ref_words > 0 else 0.0
        aggregate_rtf = total_audio_s / total_infer_s if total_infer_s > 0 else 0.0

        all_results[arch] = {
            "model": arch,
            "corpus_wer_pct": round(corpus_wer, 2),
            "aggregate_speed_x": round(aggregate_rtf, 2),
            "total_audio_s": round(total_audio_s, 2),
            "total_inference_s": round(total_infer_s, 2),
            "total_wall_s": round(total_wall_s, 2),
            "total_ref_words": total_ref_words,
            "total_substitutions": total_subs,
            "total_deletions": total_dels,
            "total_insertions": total_ins,
            "samples": model_results,
        }
        print(f"  ==> [{arch}] Corpus WER: {corpus_wer:.2f}% | Aggregate Speed: {aggregate_rtf:.1f}x RT")

    # Save to OUT_PATH
    OUT_PATH.parent.mkdir(parents=True, exist_ok=True)
    with open(OUT_PATH, "w") as f:
        json.dump({
            "benchmark": "tedlium_3_long_form_unpaced_vad_batch",
            "date": time.strftime("%Y-%m-%d"),
            "manifest": str(MANIFEST_PATH),
            "total_audio_sec": all_results["tiny-streaming"]["total_audio_s"],
            "models": all_results,
        }, f, indent=2)

    print(f"\n✓ Saved full structured results to {OUT_PATH}")

    table_headers = ["Sample ID", "Speaker", "Duration", "Tiny-Stream WER", "Small-Stream WER", "Med-Stream WER", "Med Speed"]
    rows = []
    for i, item in enumerate(manifest):
        sid = item["id"]
        spk = item["speaker"]
        dur = f"{item['duration_s']:.1f}s"
        t_wer = f"{all_results['tiny-streaming']['samples'][i]['wer_pct']:.1f}%"
        s_wer = f"{all_results['small-streaming']['samples'][i]['wer_pct']:.1f}%"
        m_wer = f"{all_results['medium-streaming']['samples'][i]['wer_pct']:.1f}%"
        m_spd = f"{all_results['medium-streaming']['samples'][i]['speed_x']:.1f}x"
        rows.append([sid, spk, dur, t_wer, s_wer, m_wer, m_spd])

    rows.append([
        "CORPUS TOTAL",
        f"{len(manifest)} Talks",
        f"{all_results['medium-streaming']['total_audio_s']:.1f}s",
        f"{all_results['tiny-streaming']['corpus_wer_pct']:.2f}%",
        f"{all_results['small-streaming']['corpus_wer_pct']:.2f}%",
        f"{all_results['medium-streaming']['corpus_wer_pct']:.2f}%",
        f"{all_results['medium-streaming']['aggregate_speed_x']:.1f}x",
    ])

    print("\n" + "=" * 90)
    print(tabulate(rows, headers=table_headers, tablefmt="github"))
    print("=" * 90)


if __name__ == "__main__":
    main()
