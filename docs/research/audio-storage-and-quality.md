# Audio recording quality and storage format (2026-11-23)

Investigation into why recorded `.wav` meeting files sounded muffled compared to headset audio and how to shrink per-hour storage without increasing recording CPU load or losing reliability.

## 1. Signal audit of the previous 16 kHz WAV pipeline

Audited `internal/capture` (`stream_windows.go`, `format.go`, `track.go`), `internal/audio/dsp/resample.go`, `internal/audio/wav`, and `internal/playback/player_windows.go`, plus measured active endpoints and a real recording (`testdata/private/interview-2026-11-22/me.wav` and `them.wav`, 399.8 s each, compared against `testdata/meetings/ami-IS1009a/them.wav`):

| Track | Format | Peak | Active RMS | Unique int16 levels | Mid-speech zero dropouts | 6–7 kHz rel. to 0–1 kHz |
|---|---|---:|---:|---:|---:|---:|
| `interview/me.wav` | 16 kHz mono | −12.0 dBFS | −32.8 dBFS | 7,305 / 65,536 | **0** | −21.8 dB |
| `interview/them.wav` | 16 kHz mono | −27.1 dBFS | −43.4 dBFS | 2,257 / 65,536 | **0** | −33.6 dB |
| `ami-IS1009a/them.wav` | 16 kHz mono | −0.2 dBFS | −22.8 dBFS | 37,731 / 65,536 | **0** | −26.9 dB |

### Findings

1. **No resampler or aligner bugs:** Zero clipped samples and zero mid-speech zero-run dropouts; `dsp.Resampler` (`defaultHalfWidth = 32`, Kaiser `beta = 8.6`, cutoff `0.94 × Nyquist`) is ±0.2 dB flat up to 6.5 kHz with >80 dB stopband rejection.
2. **Primary cause of muffled playback — 16 kHz sample rate:** Storing tracks at 16 kHz hard-cuts everything above ~7.5 kHz, stripping consonant crispness (*s, f, t, sh*) and vocal presence that 48 kHz headset audio and modern fullband call codecs (Opus) preserve. This does **not** hurt ASR (`transcribe.cpp` requires 16 kHz mono), but makes kept recordings sound like wideband telephony.
3. **8-channel surround downmix bug (`−12 dB` attenuation):** On the test PC, `SteelSeries Sonar - Gaming` (Windows default render) and `Sonar - Media` expose `96000 Hz, 8 ch, 32-bit float (mask=0x63f)`. The old `ToMono` divided the channel sum by `f.Channels` (`1/8`), losing **12 dB** whenever stereo call audio populated only Front-Left and Front-Right. Combined with low endpoint volume, this risks pushing quiet voices toward Silero VAD's silence threshold.
4. **Stereo/spatial comb filtering & legacy MME playback:** Headsets with spatial audio layers (THX Spatial Audio on `BlackShark V3 Pro XBOX - Game`, SteelSeries Sonar) introduce inter-channel phase offsets; collapsing `them` to mono comb-filters high frequencies. Playing 16 kHz mono back via `waveOutOpen` (`waveMapper`) added legacy MME upsampling on top.

## 2. Codec & format benchmarks (14-min AMI meeting, both tracks)

Probed Windows 11 Media Foundation MFTs on the test PC: `mfplat.dll`, `mfreadwrite.dll`, `mfAACEnc.dll`, `MSFlacEncoder.dll`, `MSFlacDecoder.dll`, `MSOpusDecoder.dll` are all built-in (`MFT AAC/FLAC/MP3/ALAC encoders=1`; `MFT Opus encoders=0`).

Measured on `testdata/meetings/ami-IS1009a` (`me.wav` + `them.wav`, 838.8 s each), plus Parakeet Ultra (`parakeet-ultra-Q8_0`) + Sortformer (`sortformer-4spk-v2.1`) round-trip accuracy on `ami-IS1009a-5min` (Vulkan RX 7800 XT):

| Format (both tracks) | Size / hr | Encode speed & CPU | WER `me` / `them` | DER `them` | Speaker word acc. |
|---|---:|---:|---:|---:|---:|
| 16 kHz PCM WAV (previous) | **220 MiB/hr** | ~0% | 15.6% / 19.3% | 51.6% | 78.2% |
| 16 kHz FLAC (level 5) | 57.0 MiB/hr | ~2000× RT | identical | identical | identical |
| **MF AAC 48 kHz mono 48 kbps** | **42.5 MiB/hr** | **216× RT (~0.46% of 1 core)** | **13.3% / 17.0%** | **50.4%** | **80.0%** |
| **MF AAC 48 kHz mono 64 kbps** | **56.2 MiB/hr** | **206× RT (~0.48% of 1 core)** | **14.4% / 19.3%** | **51.1%** | **79.5%** |
| MF AAC 48 kHz mono 96 kbps | 83.7 MiB/hr | 222× RT (~0.45% of 1 core) | — | — | — |
| Opus 48 kHz mono 24 kbps (voip) | 13.8 MiB/hr | 67× RT (~1.5% of 1 core) | 15.3% / 18.3% | 49.9% | 84.9% |
| Opus 48 kHz mono 32 kbps (audio) | 18.3 MiB/hr | 71× RT (~1.4% of 1 core) | 15.3% / 18.8% | 31.4% | 98.2% |

### Direct Media Foundation MFT verification (`internal/audio/m4a`)

Calling `CLSID_AACMFTEncoder` (`{93AF0C51-2275-45D2-A35B-F2BA21CAED00}`) and `CLSID_CMSAACDecMFT` (`{32D186A7-218F-4C75-8876-DD77273A8999}`) directly via COM without cgo:
- **Zero net priming lag:** Cross-correlating a 1 kHz windowed pulse round-tripped through `CLSID_AACMFTEncoder` → `CLSID_CMSAACDecMFT` measured **0 samples (0.00 ms)** residual delay (`TestPrimingAlignment`).
- **Bitrate negotiation:** `me.m4a` uses 48 kHz mono 64 kbps (~28 MiB/hr, with automatic fallback to 96/128 kbps on older Windows 10 builds that enforce higher mono floors); `them.m4a` uses 48 kHz stereo 96 kbps (~42 MiB/hr) — **~70 MiB/hr combined** (>3× smaller than 16 kHz WAV, fullband 48 kHz, stereo meeting track).
- **Container compliance:** Pure-Go fast-start MP4 (`ftyp` + `moov` + `mdat`) validates cleanly with `ffmpeg -v error -i … -f null -` (`TestFFmpegValidatesM4A`).
- **Crash safety:** While recording, frames stream with 7-byte ADTS headers into `<track>.m4a.aac` (flushed every 2 s) so a crash loses at most 2 s and `m4a.Recover` remuxes leftover `.aac` files into `.m4a` without re-encoding.
