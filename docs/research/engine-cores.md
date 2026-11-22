# How many cores the engine uses, and what limiting it costs

Questions (2026-11-22): the sidebar's core squares showed one core during a transcription after a meeting, where the author expected eight. How many does the engine really keep busy, why did the squares miss them, and what does a cap on the transcription after a meeting cost in time?

Code: `internal/sysinfo` (`ReadThreads`, `CoreMeter`), `internal/app/debug_windows.go`, the limit in `internal/app/jobs_windows.go` and `live_windows.go`. Design: [design.md](../design.md) (the sidebar, Settings → Transcription).

## Measured

All on the author's PC (Windows 11, 8 logical processors as Go sees them), the console tool `flit transcribe` in a real engine worker, parakeet-ultra, `testdata/meetings/ami-IS1009a-5min/them.wav` (300 s), engine threads left to transcribe.cpp's default unless given.

**The worker's threads, sampled every 250 ms** (PowerShell, `Get-Process` threads, a thread counted busy above 5% of the interval): 16–20 threads alive, up to 8 busy at once (5.6 on average), and **188 different threads over the 22 s run**: the engine's thread pool replaces about 7 threads every one to three seconds.

That is why the squares showed one core: the meter compared each thread's CPU time between two readings a second apart, and a thread that wasn't in the previous reading didn't count. With the pool replaced that often, only the main thread was ever in both.

**The meter now** (once a second, as the sidebar reads it) counts a thread started since the last reading with all its CPU time (`SYSTEM_THREAD_INFORMATION.CreateTime`), and spreads the CPU the visible threads don't account for over the squares (the processes' own `UserTime + KernelTime` include threads that have ended): first filling partly busy ones, then adding more, at most `runtime.NumCPU()` of them. On the same run it showed 7–8 cores, 6–7 cores' worth (`TestCoreMeterThreadChurn` keeps the case).

**Limiting the transcription after a meeting** (the cap Windows enforces with a job object, below normal priority, threads matched to the cap, as for live transcription; `--cpu-limit N --threads M`):

| Limit | Time for 300 s | Real time | Cores shown, on average | Cores' worth |
|---|---|---|---|---|
| none (default threads) | 20.1 s | 15.1× | 7.9 | 6.4 |
| 50%, 4 threads (Balanced) | 24.2 s | 12.5× | 4.4 | 3.8 |
| 25%, 2 threads (Light) | 43.7 s | 6.9× | 2.2 | 1.9 |

Half the processor costs a fifth more time: the engine doesn't use 8 threads fully (6.4 cores' worth). A quarter of it more than doubles the time. A one-hour meeting would take about 4 min unlimited, 5 min at Balanced and 9 min at Light (extrapolated from these 5 minutes, unverified).

## Decision

Settings → Transcription → **Limit the transcription after a meeting**, off by default (as before): Light 25%, Balanced 50% (the default once on), Faster 75% of the processor. The next engine worker keeps to it; one already running with another limit is replaced at the next job.
