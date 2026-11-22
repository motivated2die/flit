# Transcribing during the meeting

Question (2026-11-21): can flit transcribe while the meeting goes on, so the transcript is ready soon after it ends, without lowering accuracy and without slowing the PC (the meeting app needs it)? And where does telling speakers apart belong?

Answer: yes, built as an option (Settings → Transcribe during meetings, off by default; Background load: Light 15%, Balanced 25%, Faster 40% of the processor). Decision: [ADR 0007](../decisions/0007-live-transcription.md).

## Accuracy: the same chunks as afterwards

Live transcription reuses the batch design ([long-form-chunking.md](long-form-chunking.md)): pause-aligned chunks of about 30 s (5–40 s), each transcribed once. A chunk is transcribed only once its cut is certain (`chunk.Plan` with `final=false` returns only cuts more audio can't change: 40 s + 5 s lookahead past the chunk's start).

To make the live result *equal* the batch result, not just similar, every cut is decided on a fresh window of audio (`pipeline.Progressive`, `decideWindow` = 60 s from the last cut), starting on the track's grid of VAD frames (512 samples) with 16 frames of warm-up for the VAD's state. Silero VAD is causal (a frame's score depends only on earlier audio), so a window that ends at "now" (live) and one that ends 60 s later (afterwards) give the same scores for every frame the decision uses. Batch transcription runs the very same code once with `final=true`.

Measured (2026-11-21, Parakeet Ultra, `flit transcribe --live-step 15` feeding each track 15 s at a time against a run over the whole track):

| Meeting | Live vs afterwards | WER Me / Them | Left at the end |
|---|---|---|---|
| de-synth-4spk (5:22) | byte-identical transcript | 8.9% / 10.7% | 1 chunk per track, 1.0–2.3 s |
| ami-IS1009a-5min | byte-identical transcript | 15.6% / 19.3% | 1 chunk per track, 2.2–2.4 s |

The windowed planning changed the batch result slightly against the earlier whole-track VAD pass (±1 point on the 5-minute tests, within noise); on the 14-minute AMI meeting it is a little better (12.7% / 23.8% against 12.8% / 24.2%). A first version without the shared frame grid and fresh windows differed from batch by up to 1 point WER (VAD state at different window starts).

## Load: a hard cap, steady

The live transcription gets its own engine worker, started with the recording and closed after it:

- **Hard CPU cap**: the worker runs in a Windows job object with `JOB_OBJECT_CPU_RATE_CONTROL_HARD_CAP` at the chosen share of all processors. Windows enforces it; however much work is queued, the worker never uses more.
- **Below-normal priority**: the meeting app, the browser and everything the user works with come first.
- **Threads matched to the cap** (`round(processors × share)`, at least 1): more threads than the cap allows only take turns and are slower (measured below).
- **Paced by the meeting**: every 15 s the recording is looked at; a chunk becomes certain about every 30 s of each track. The work arrives at the meeting's pace, not in a burst at the end.

Measured throughput under the cap (8 logical processors, both tracks of ami-IS1009a-5min, `--cpu-limit`):

| Cap (threads) | Per track | Both tracks need, of the meeting's length |
|---|---:|---:|
| none (8) | 19× real time | 11%, in bursts at full load |
| 50% (4) | 13× | 15% |
| 30% (2) | 7.1× | 28% |
| 30% (8 threads) | 6.1× | 33% |
| 15% (1) | 3.6× | 56% |

So on this PC even Light keeps up with a meeting (it needs about half the meeting's time at 15%). A PC with half the speed per core would need about all of it at Light and keep up comfortably at Balanced. When live transcription can't keep up, nothing breaks: it stays behind, keeps working at the cap, and the end transcribes the rest at full speed.

The WER under every cap is the same as without (same chunks, same engine).

A meeting in real time (`flit transcribe --live-step 15 --realtime --cpu-limit 15 --threads 1`, both tracks of ami-IS1009a-5min fed at their own pace, stepped every 15 s as the app does): the transcript stayed **25–44 s behind** the meeting throughout (the planner's lookahead: a chunk is certain 45 s after it starts), the engine used **at most 13.4% and about 7% on average** of the machine (cap 15%), its steps took 0.1–18 s each, and at the end 2 chunks were left. In this run the end was done in 21 s because the simulation kept the cap; the app finishes at full speed (2–4 s for those chunks, measured above). WER equal to the transcript made afterwards (15.6% / 19.3%).

## Speakers: at the end

Telling speakers apart stays after transcription, at the end:

- Sortformer, as transcribe.cpp offers it (`transcribe_diarize_run`), takes a whole recording and numbers speakers by first appearance in **one pass**; that one pass is what keeps "Speaker 2" the same person for the whole meeting ([diarization.md](diarization.md)). Running it on the growing track again and again would cost more each time (the whole track every time) and could renumber speakers between runs.
- It is cheap at the end: about 30× real time on the CPU (2 minutes per hour of meeting), against ~19× per track for transcription. With live transcription on, it is most of what is left when the meeting ends.
- Words are assigned to speakers after both (`transcript.AssignSpeakers`); it needs the transcript's word times, which live transcription already has.

Later: transcribe.cpp plans a push-audio streaming entry point for Sortformer (`transcribe_stream_begin/feed`, docs/porting/families/sortformer.md "A future push-audio entry point … live diarization"). Its speaker cache keeps identities as audio streams in, so diarization could then run live under the same cap, leaving almost nothing for the end.

## Reliability

- Live transcription's state (`live.json`: how far each track is, its segments, the language and model) is saved after every chunk. The final transcription goes on from it and saves after every chunk too; a crash or quit at any point resumes there. State made for another language or model is dropped (its text would be wrong).
- The final transcription starts only after live transcription has stopped (it is cancelled when the recording ends; a chunk in progress is dropped and done again at the end).
- Transcribe again and Find speakers start from the beginning (live.json is removed).
- Without live transcription nothing changes for the user, but the batch path is now the same chunk-by-chunk code, reading the files instead of loading an hour of audio into memory at once (diarization still loads the meeting's track).
