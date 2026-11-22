# Where the voice meter's bands sit

Question (2026-11-22): the author found the voice meter's bars leaning to the left, as if their microphone's voice were too deep. Which frequency layout puts speech in the middle of the 9 bars, without making the bars move on room noise?

Code: `internal/capture/spectrum.go` (`bandLayout`, `voiceBands`). Design: [design.md](../design.md) (the voice meter). The first layout followed Handy's ([references/handy.md](../references/handy.md#the-recording-overlays-waveform)).

## How it was measured

A throw-away test fed four 16 kHz recordings from the repository through the analyser, 512-sample windows, the first 120 s of each: `testdata/jfk.wav` (English, broadcast), `testdata/fleurs-de.wav` (German, read speech), and the mic tracks of two test meetings (`testdata/meetings/ami-IS1009a-5min/me.wav`, `de-synth-4spk/me.wav`). Over the windows where someone speaks (the loudest bar at least 0.25):

- **centre**: the bars' centre of mass, 0..8 (4 is the middle);
- **left**: how often the loudest bar is one of the first three;
- **saturated**: how often a bar is pinned at the top.

Room noise was measured separately: uniform white noise (the worst case for lifting high bands) at -60, -50 and -45 dBFS RMS for 5 s, the highest any bar reached.

## Results

On the three microphone-style recordings (FLEURS and the two meeting tracks; JFK is a loud broadcast and less telling):

| Layout | centre | left | saturated | white noise -50 dBFS, bars up to |
|---|---|---|---|---|
| 300–2400 Hz, quadratic, no tilt (the first one) | 2.47–2.77 | 80–86% | 0.4–3% | 0.00 |
| Handy's visible bars (400–1540 Hz, quadratic) | 2.73–2.90 | 65–81% | 1–4% | not measured |
| 300–4000 Hz, even spacing | 1.60–2.20 | 91–97% | 0.2–2% | not measured |
| 400–4000 Hz, spacing 1.5, +6 dB/octave | 3.77–3.84 | 62–73% | 1–6% | 0.45 |
| same, with a 6 dB gate over each band's noise floor | 3.77–3.84 | 62–73% | 1–6% | 0.10 |
| 250–4000 Hz, spacing 1.5, +6 dB/octave | 3.73–3.91 | 60–72% | 2–8% | 0.57 |

Findings:

- Widening the range alone makes it worse: speech is much weaker above 2 kHz, so more bands sit nearly empty on the right.
- Starting lower (100–150 Hz) doesn't centre the voice either: these voices are strong from their fundamental up through the first formant, so the low bars stay loudest.
- What moves the weight is a **tilt**: raising each band by its octaves above the range's start (+6 dB per octave) compensates speech's fall-off; the centre moves from about 2.6 to 3.8.
- The tilt also lifts hiss: steady white noise at -50 dBFS moved the right bars to 0.45, at -45 dBFS to 0.61. A **gate** fixes it: each band is measured from 6 dB above its noise floor, the lowest level it had in the previous 64 windows (about 2 s; speech pauses at least that often, steady noise doesn't). With it, noise reaches at most 0.10 at -50 dBFS and 0.17 at -45 dBFS (mostly in the first 2 s, before the floor is known); speech reads the same.

## Decision

`voiceBands = {lo: 400, hi: 4000, spacing: 1.5, tilt: 6, gate: 6}`. Kept as tests: `TestSpectrumSpeechCentred` (FLEURS: centre at least 3.4) and `TestSpectrumIgnoresSteadyNoise` (white noise at -50 dBFS: bars at most 0.12 after 3 s). The analyser costs 7.9 µs a window with the gate (`BenchmarkSpectrumWindow`, was 7.3 µs).

Unverified: real room tone is closer to pink or brown noise than white (less energy high), so the gate should have less to do than in the test; and how it looks with the author's own microphone, which only a recording can show.
