# 0013: An own, line-based Markdown notes editor on flit's Direct2D framework

- **Status:** accepted
- **Date:** 2026-11-22

## Context

Recordings get notes the user types or pastes: headings 2 and 3, nested bullets, numbered items, task checkboxes, paragraphs, **bold** and *italics*, styled as Markdown usually renders, with Markdown pasted in and Markdown or plain text copied out; and `@flit` mentions, instructions to flit at a moment of the meeting. The notes feed the summaries, which need to know when each line was typed during a recording. flit draws its window itself with Direct2D/DirectWrite and had a one-line text field, paragraph hit testing and cross-paragraph selection, but no multi-line editor ([research/notes-editor.md](../research/notes-editor.md)).

## Options

1. **Own line-based editor**: blocks of the Markdown kinds with an indent level and inline Markdown kept as typed; Markdown as the file format; drawn like the rest of flit.
2. **RichEdit** (Windows' `msftedit.dll`) as a child window: mature editing and accessibility, but no Markdown, no checkboxes, an older copy than Office's, and a GDI child window in a drawn UI.
3. **Scintilla** with a Markdown lexer: fast and mature, but a source editor (markers visible), one line height for all lines, no clickable checkboxes.
4. **WebView2** with a JavaScript editor (TipTap, Milkdown, CodeMirror): the best editing, at the cost of a browser engine for a notes box, against [0001](0001-stay-with-go.md).

## Decision

**1**, built 2026-11-22: `internal/notes` (document, Markdown in and out, plain text, HTML, the editing rules, undo, mentions, typing times), `internal/ui` (`NotesEditor`, styled DirectWrite paragraphs, the window's keyboard focus for editors), `internal/app` (the Notes section of a recording, the notch's and Home's Notes buttons, saving). Markdown storage: one block per line, a blank line between blocks and none between list items, nested items under their parent's text; mentions as `[@flit 12:40: …]`; typing times beside it in `notes.json`. Scintilla stays the fallback.

## Consequences

- No new runtime or DLL; the editor looks like flit and can serve other multi-line inputs (vocabulary).
- flit owns caret movement, selection, undo and clipboard formats: about 2,500 lines of Go plus 1,000 of tests, more than the 1,200–1,800 estimated, because bold, italics, numbered lists, mentions, typing times and HTML copy were added.
- IME composition and screen-reader support are missing, as elsewhere in flit; HTML on paste is missing (Markdown and plain text with markers keep their structure).
- Inline formatting shows its Markdown markers faintly instead of hiding them, which keeps the caret simple and the text exactly what is stored.

## Revisit when

- Users need IME input or a screen reader in the notes.
- The editing details cost far more to maintain than they save: Scintilla, or a richer model.
