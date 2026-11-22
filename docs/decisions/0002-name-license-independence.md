# 0002: Name "flit", MIT license, no code ties to other projects

- **Status:** accepted
- **Date:** 2026-11-21

## Context

The app needed a name (install folder, registry keys, mutex, window classes) and a license before the first line of code. The author owns Resound's code, but the two projects should stay independent. AutoDoc is AGPL-3.0, Handy and transcribe.cpp are MIT.

## Decision

- The app is called **flit** (lower case in identifiers and paths: `flit.exe`, `%LOCALAPPDATA%\flit`, `Local\flit.SingleInstance`; "flit" in the UI).
- flit is licensed under the **MIT license**.
- **flit's code is its own.** We re-implement what we need, imitating strategies that work well in Resound, Handy and AutoDoc, and build our own small libraries, components and animations. No module dependency on Resound, no shared code module.
- Where another project's license would not allow copying (AutoDoc, AGPL), we take ideas only. Third-party libraries we *depend on* (go-wca, go-ole, x/sys, transcribe.cpp's DLLs) are listed with their licenses in `THIRD_PARTY_NOTICES.md`, as are model licenses (CC-BY-4.0 for Parakeet).

## Consequences

- Resound's documented behaviour and measurements (its `ARCHITECTURE.md`) are our design reference; the code is written fresh in flit, shaped by flit's needs.
- Fixes found in one project don't flow to the other automatically.
