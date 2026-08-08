package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
)

type BenchmarkReport struct {
	Timestamp         string  `json:"timestamp"`
	Target            string  `json:"target"`
	ConcurrentStreams int     `json:"concurrent_streams"`
	Duration          string  `json:"duration"`
	TotalAudioSec     float64 `json:"total_audio_sec"`
	TotalAudioHours   float64 `json:"total_audio_hours"`
	AggregateRTF      float64 `json:"aggregate_rtf"`
	Encoding          string  `json:"encoding"`
	SampleRate        int     `json:"sample_rate"`
	ChunkMs           int     `json:"chunk_ms"`

	TTFTP50Ms int64 `json:"ttft_p50_ms"`
	TTFTP90Ms int64 `json:"ttft_p90_ms"`
	TTFTP95Ms int64 `json:"ttft_p95_ms"`
	TTFTP99Ms int64 `json:"ttft_p99_ms"`

	InterimP50Ms int64 `json:"interim_p50_ms"`
	InterimP90Ms int64 `json:"interim_p90_ms"`
	InterimP95Ms int64 `json:"interim_p95_ms"`
	InterimP99Ms int64 `json:"interim_p99_ms"`

	FinalizedP50Ms int64 `json:"finalized_p50_ms"`
	FinalizedP90Ms int64 `json:"finalized_p90_ms"`
	FinalizedP95Ms int64 `json:"finalized_p95_ms"`
	FinalizedP99Ms int64 `json:"finalized_p99_ms"`

	BaselineRSSMB float64 `json:"baseline_rss_mb,omitempty"`
	PeakRSSMB     float64 `json:"peak_rss_mb,omitempty"`
	FinalRSSMB    float64 `json:"final_rss_mb,omitempty"`
}

func renderReport(cfg Config, stats []*StreamStats, wallClock time.Duration, baselineRSS, peakRSS, finalRSS int64) {
	combinedTTFT := hdrhistogram.New(1, 30000, 3)
	combinedInterim := hdrhistogram.New(1, 30000, 3)
	combinedFinalized := hdrhistogram.New(1, 30000, 3)

	var totalInterimCount, totalFinalizedCount int64

	for _, s := range stats {
		_ = combinedTTFT.Merge(s.TTFTHistogram)
		_ = combinedInterim.Merge(s.InterimLatencyHist)
		_ = combinedFinalized.Merge(s.FinalizedLatencyHist)
		totalInterimCount += s.InterimEvents
		totalFinalizedCount += s.FinalizedLines
	}

	totalAudioSec := float64(cfg.Streams) * cfg.Duration.Seconds()
	totalAudioHours := totalAudioSec / 3600.0
	rtf := wallClock.Seconds() / totalAudioSec

	report := BenchmarkReport{
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		Target:            cfg.Target,
		ConcurrentStreams: cfg.Streams,
		Duration:          cfg.Duration.String(),
		TotalAudioSec:     totalAudioSec,
		TotalAudioHours:   totalAudioHours,
		AggregateRTF:      rtf,
		Encoding:          cfg.Encoding,
		SampleRate:        cfg.SampleRate,
		ChunkMs:           cfg.ChunkMs,

		TTFTP50Ms: combinedTTFT.ValueAtQuantile(50),
		TTFTP90Ms: combinedTTFT.ValueAtQuantile(90),
		TTFTP95Ms: combinedTTFT.ValueAtQuantile(95),
		TTFTP99Ms: combinedTTFT.ValueAtQuantile(99),

		InterimP50Ms: combinedInterim.ValueAtQuantile(50),
		InterimP90Ms: combinedInterim.ValueAtQuantile(90),
		InterimP95Ms: combinedInterim.ValueAtQuantile(95),
		InterimP99Ms: combinedInterim.ValueAtQuantile(99),

		FinalizedP50Ms: combinedFinalized.ValueAtQuantile(50),
		FinalizedP90Ms: combinedFinalized.ValueAtQuantile(90),
		FinalizedP95Ms: combinedFinalized.ValueAtQuantile(95),
		FinalizedP99Ms: combinedFinalized.ValueAtQuantile(99),

		BaselineRSSMB: float64(baselineRSS) / (1024 * 1024),
		PeakRSSMB:     float64(peakRSS) / (1024 * 1024),
		FinalRSSMB:    float64(finalRSS) / (1024 * 1024),
	}

	if cfg.ReportFormat == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return
	}

	// Markdown report output
	fmt.Println()
	fmt.Println("# moonshine serve Load Benchmark Report")
	fmt.Println()
	fmt.Printf("- **Date:** %s\n", report.Timestamp)
	fmt.Printf("- **Target:** `%s`\n", report.Target)
	fmt.Printf("- **Concurrent Streams:** %d\n", report.ConcurrentStreams)
	fmt.Printf("- **Audio Format:** %dHz %s (%dms chunks)\n", report.SampleRate, report.Encoding, report.ChunkMs)
	fmt.Printf("- **Test Duration:** %s (Total Audio: %.1fs / %.3f hrs)\n", report.Duration, report.TotalAudioSec, report.TotalAudioHours)
	fmt.Printf("- **Aggregate Real-Time Factor (RTF):** %.3f (Wall-clock: %v)\n", report.AggregateRTF, wallClock.Round(time.Millisecond))
	fmt.Println()

	fmt.Println("## Latency Percentiles")
	fmt.Println()
	fmt.Println("| Metric | P50 (ms) | P90 (ms) | P95 (ms) | P99 (ms) | Total Events |")
	fmt.Println("|---|---|---|---|---|---|")
	fmt.Printf("| **Time to First Transcript (TTFT)** | %d | %d | %d | %d | %d |\n",
		report.TTFTP50Ms, report.TTFTP90Ms, report.TTFTP95Ms, report.TTFTP99Ms, combinedTTFT.TotalCount())
	fmt.Printf("| **Interim Poll Latency** | %d | %d | %d | %d | %d |\n",
		report.InterimP50Ms, report.InterimP90Ms, report.InterimP95Ms, report.InterimP99Ms, totalInterimCount)
	fmt.Printf("| **Finalized Line Latency** | %d | %d | %d | %d | %d |\n",
		report.FinalizedP50Ms, report.FinalizedP90Ms, report.FinalizedP95Ms, report.FinalizedP99Ms, totalFinalizedCount)
	fmt.Println()

	if cfg.ServerPID > 0 {
		fmt.Println("## Server Process Memory (OS RSS)")
		fmt.Println()
		fmt.Printf("- **Baseline RSS:** %.1f MB\n", report.BaselineRSSMB)
		fmt.Printf("- **Peak RSS:** %.1f MB\n", report.PeakRSSMB)
		fmt.Printf("- **Final RSS:** %.1f MB\n", report.FinalRSSMB)
		fmt.Println()
	}

	fmt.Println("## SLO Verdict")
	fmt.Println()
	if report.InterimP95Ms <= 2000 {
		fmt.Printf("✅ **PASS**: P95 interim latency (%dms) <= 2,000ms SLO threshold at %d concurrent streams.\n",
			report.InterimP95Ms, report.ConcurrentStreams)
	} else {
		fmt.Printf("❌ **EXCEEDED**: P95 interim latency (%dms) > 2,000ms SLO threshold at %d concurrent streams.\n",
			report.InterimP95Ms, report.ConcurrentStreams)
	}
	fmt.Println()
}
