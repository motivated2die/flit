# 0015. Store recordings as 48 kHz M4A (AAC-LC) via Windows Media Foundation

Date: 2026-11-23
Status: **Accepted**

## Context

flit previously downsampled both tracks (`me.wav`, `them.wav`) to 16 kHz mono 16-bit PCM WAV during recording:
- Two tracks consumed **220 MiB (230 MB) per hour** of disk space.
- Downsampling to 16 kHz permanently cut everything above ~7.5 kHz, making kept recordings sound muffled compared to live 48 kHz headset audio.
- Collapsing multi-channel loopback endpoints with `sum / channels` attenuated 7.1 virtual surround devices (e.g. SteelSeries Sonar 8 ch) by **−12 dB** and introduced stereo/spatial comb filtering on the meeting track.

See [research/audio-storage-and-quality.md](../research/audio-storage-and-quality.md) for all device, spectral, codec, CPU, and WER measurements.

## Decision

1. **Record and store tracks at 48 kHz AAC-LC in `.m4a` using Windows Media Foundation (`mfplat.dll`, no cgo, no external binaries):**
   - `me.m4a`: **48 kHz mono, 64 kbps** (auto-falling back to 96/128 kbps if an older Windows 10 build rejects 64 kbps mono).
   - `them.m4a`: **48 kHz stereo, 96 kbps** (preserves the headset's stereo image and avoids spatial/HRTF mono comb filtering).
   - Combined size: **~70 MiB/hr** (~3.1× smaller than 16 kHz WAV) at **<0.5% of 1 CPU core** encoding overhead and **0% WER/DER impact** on Parakeet + Sortformer.
2. **Crash-safe ADTS streaming with lossless M4A remux:**
   - During recording, `internal/audio/m4a.Writer` writes 7-byte ADTS headers + raw AAC frames to `<track>.m4a.aac` and syncs every 2 s. Live transcription reads the growing `.aac` file concurrently.
   - On `Close()` (and in `session.Recover` after a crash), a pure-Go MP4 muxer wraps the frames into a standard fast-start seekable `.m4a` file atomically without re-encoding and removes `.m4a.aac`.
3. **Automatic WAV fallback and full backwards compatibility:**
   - On systems without Media Foundation AAC (e.g. Windows N editions without the Media Feature Pack), `m4a.Available()` returns `false` and recordings transparently fall back to WAV.
   - `internal/audio/track` reads `.m4a`, growing `.aac`, and legacy `.wav` uniformly, resampling to 16 kHz mono only when feeding `transcribe.cpp`.
4. **ITU-R BS.775 surround downmix & 48 kHz stereo playback:**
   - `internal/capture/format.go` folds 5.1/7.1 endpoints per ITU-R BS.775 (skipping LFE) instead of dividing by channel count, eliminating the −12 dB attenuation on 8-channel devices.
   - `internal/playback` plays at **48 kHz stereo** (`waveOut`), passing 48 kHz `.m4a` tracks through without resampling and upsampling legacy 16 kHz `.wav` recordings cleanly.

## Alternatives considered

- **Opus (`.opus`)**: Smallest files (~14–18 MiB/hr), zero WER loss, but Windows 10/11 ships no built-in Opus encoder (`MFT Opus encoders = 0`). Would require bundling `libopus.dll` or an unvetted pure-Go Opus encoder with higher CPU load.
- **FLAC (lossless)**: Fast and built into Windows (`MSFlacEncoder.dll`), but cannot solve both issues at once — 16 kHz FLAC remains muffled (57 MiB/hr) while real 48 kHz FLAC remains large (~180–280 MiB/hr).
- **`IMFSinkWriter` directly to `.m4a`**: Risk of losing the entire recording on crash because the MP4 `moov` atom is only written at `Finalize()`. Streaming ADTS via `CLSID_AACMFTEncoder` + pure-Go remux keeps crash safety identical to WAV.

## Consequences

- Kept recordings are ~3× smaller, sound fullband and stereo like the live headset, and play natively in Windows Explorer/Media Player/VLC.
- Existing sessions with `me.wav` / `them.wav` continue to play, re-transcribe, and diarize with no migration required.
