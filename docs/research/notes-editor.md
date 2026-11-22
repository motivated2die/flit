# A small notes editor

Question (2026-11-22): the user should be able to take or paste notes on a recording, in a small editor with exactly these elements: **heading 2, heading 3, nested bullet points, task checkboxes** (plus plain paragraphs; bold, italics and numbered lists added by the author after the research), styled the way Markdown usually renders. Paste Markdown in; copy Markdown or plain text out. Is there a ready component that fits flit, or should flit build a light one? The notes feed the summaries ([summary-pipeline.md](summary-pipeline.md)), so the editor also records when each line was written during a recording.

## What flit's UI has (2026-11-22)

flit draws its whole window itself with Direct2D and DirectWrite, without cgo or a web view ([ADR 0001](../decisions/0001-stay-with-go.md), [ADR 0005](../decisions/0005-drawn-caption-buttons.md), [design.md](../design.md)). Already there:

- `ui.Editing` and `ui.TextField` (`internal/ui/textfield.go`): a **one-line** field: caret, select-all, insert, backspace/delete, Home/End/arrows, Enter commits, Esc cancels, Ctrl+V pastes the first line; WM_CHAR with surrogate pairs (`internal/ui/host_windows.go`, `typed`).
- `ui.TextLines` (`internal/ui/textsel.go`): a wrapped paragraph's lines with **caret stops** from DirectWrite's line and cluster metrics (`d2d.ParagraphLines`), hit testing (`PosAt`), selection rectangles (`Rects`), word boundaries (`wordAt`); **selection across paragraphs** with the pointer (the transcript's).
- `Painter.MarkedParagraph` (`internal/ui/d2d_windows.go`): a paragraph with styled ranges.
- `SetClipboard` / `ClipboardText` (`internal/ui/clipboard_windows.go`): Unicode text only.

Missing for an editor: multi-line text with Enter and line joins, a caret moving between lines (Up/Down keeping its x), keyboard selection (Shift+arrows, Ctrl+arrows by word), undo/redo, scrolling the caret into view, IME composition (only needed for East Asian input; German and English keyboards, dead keys included, arrive as WM_CHAR), and accessibility (flit has none anywhere yet).

## Options

| Option | What it is | For | Against |
|---|---|---|---|
| **A. Own line-based editor** on flit's Direct2D framework | A list of blocks (paragraph, H2, H3, bullet, task) with an indent level and plain text; Markdown is the file format; each block drawn as a DirectWrite paragraph | Looks and behaves like the rest of flit; no new runtime or DLL; Markdown round-trips exactly because the model *is* the five Markdown constructs; reuses `TextLines`, selection and clipboard code | We own caret movement, selection, undo, clipboard formats; no IME or screen-reader support at first |
| B. **RichEdit** (`msftedit.dll`, in Windows) as a child window | The system rich text control | Mature editing, undo, IME, accessibility, clipboard (RTF, plain), part of Windows | **No Markdown support** (Murray Sargent, who maintains it, only jokes about a Markdown mode, [blog](https://devblogs.microsoft.com/math-in-office/richedit-autoformatting/)); the Windows copy is "pretty dated" next to Office's, which isn't redistributable ([blog](https://devblogs.microsoft.com/math-in-office/?p=537)); no task checkboxes; Markdown ↔ RTF conversion is ours; a GDI child window inside flit's drawn window (theming, scrollbars, dark mode, focus) |
| C. **Scintilla** + Lexilla's Markdown lexer as a child window | The editor component of Notepad++ (permissive license), driven by window messages, so callable from Go without cgo; has a DirectWrite renderer | Fast, mature, undo, IME, large files; Markdown syntax colouring | A *source* editor: `##` and `- [ ]` stay visible; **all lines have one height** ("calculated from the largest font in any current style", [ScintillaDoc](https://scintilla.org/ScintillaDoc.html)), so headings can't be larger; checkboxes not clickable; ~1–2 MB of DLLs (to check) |
| D. **WebView2** with a JavaScript editor (TipTap, Milkdown, CodeMirror 6) | A browser engine in a child window | The best editing quality, ready Markdown support | An Edge WebView2 process tree for a notes box (tens to hundreds of MB, unmeasured); the look of the web inside a native app; JavaScript bundled; against ADR 0001's reasons. anarlog is moving *away* from exactly this setup ([references/anarlog.md](../references/anarlog.md#the-native-editor-appsdesktop-gpuisrceditor)) |
| E. A Go GUI toolkit's editor (Gio, Fyne) | Their text widgets | Pure Go | Their own rendering and event loop; doesn't embed in flit's window |

What others did: Yogurt uses TipTap in Electron; AutoDoc a web editor too; **anarlog wrote its own native editor** for GPUI, ~5,000 lines of core for a ProseMirror-compatible schema ([anarlog.md](../references/anarlog.md#the-native-editor-appsdesktop-gpuisrceditor)).

## Built (2026-11-22): A, deliberately small

The required elements are line-shaped, so the editor is a **list of lines, each one block**, not a rich text tree; Markdown is the file format and the model maps onto it exactly. After the research the author added **bold, italics and numbered lists**, and **@flit mentions**. Scintilla (option C) stays the fallback if the editing details ever cost too much.

### Model and storage (`internal/notes/doc.go`)

- Blocks: paragraph, H2, H3, bullet, numbered item, task (open or done); list items have an indent level (0–4); the text is one line of **inline Markdown kept as typed** (`**bold**`, `*italic*`, `__`/`_` too, mentions).
- **Markdown out** (`Doc.Markdown`, written to `notes.md`): `## `, `### `, `- `, `1. ` (numbers counted per list), `- [ ] `/`- [x] `; nested items under their parent's text (two spaces under a bullet or task, the number's width under a numbered item, so CommonMark renderers nest them); **a blank line between blocks, none between list items**; trailing spaces trimmed (they would mean a line break); a paragraph that starts like a marker is escaped with a backslash.
- **Markdown in** (`Parse`): every non-blank line is a block, as note apps treat lines (not CommonMark's joined paragraphs); lists nest by their columns (tabs are 4); `#`/`##` → H2, `###` and deeper → H3; `*`, `+`, `•` bullets; `1.` and `1)` numbers; `- [ ]`, `- [x]` and ☐/☑ tasks; an indented line under an item continues it; thematic breaks stay text. Parse → Markdown → Parse is stable (tested).
- **Plain text out** (`Doc.Plain`): headings as text, "•" bullets, numbers, ☐/☑ tasks, emphasis markers removed; pasting it back gives the same blocks.
- **HTML out** (`Doc.HTML`): headings, nested `<ul>`/`<ol>`, ☐/☑ items, `<strong>`/`<em>`, escaped. Copy puts it on the clipboard as "HTML Format" beside the Markdown text (`ui.SetClipboardRich`, `cfHTML`), so Outlook, OneNote and Word paste the structure.
- **@flit mentions**: typing `@flit` followed by a space or colon at a word's start opens a mention; the user types an instruction; **Enter (or `]`) confirms it** and the caret stays on the line, a second Enter ends the line; Esc confirms too; Enter on an empty mention removes it; Backspace at the instruction's start unwraps it into plain text. Stored as **`[@flit 12:40: instruction]`** with the session time when typed during the recording (`[@flit: …]` otherwise): the time lives in the Markdown itself, so it survives copying and outside edits, and the brackets say where the instruction ends. `notes.Mentions` reads them back (time, instruction) for the summary.
- **When each line was typed**: a line first typed while its recording runs keeps that session time (`Block.Typed`); lines typed before or after, and pasted lines, have none. Kept in `notes.json` beside `notes.md` (`notes.Times`), matched back by the line's text, so an edit elsewhere (in flit or in the file) keeps the other lines' times. A line split by Enter keeps its time for the moved half.

### Editing (`internal/notes/editor.go`)

- Markdown shortcuts at the start of any line, followed by a space: `#`/`##` → H2, `###` → H3, `-`/`*`/`+` → bullet, `1.`/`1)` → numbered, `[]`/`[ ]`/`- [ ]` → task (`[x]` done). They also switch a continued list item (after Enter) to another kind.
- Enter continues a list (an open task after a task), a heading continues with a paragraph; on an empty item it steps out a level, then out of the list; at the start of a non-empty line it opens an empty line above. Backspace at a line's start: nested item → out a level, other block → paragraph, paragraph → joins the line above. Delete at a line's end joins the next.
- Tab / Shift+Tab indent and outdent list items (at most one level deeper than the item above). Ctrl+Enter or a click on the box ticks a task (and makes a line a task). Ctrl+B / Ctrl+I wrap or unwrap the selection (an empty pair without one).
- Caret and selection: arrows, Ctrl+arrows by word, Home/End (Ctrl: the notes' start/end), Up/Down keeping their x across lines and wrapped lines, Page Up/Down, Shift to select, pointer press and drag, double click a word, triple click a line, Ctrl+A.
- Clipboard: Ctrl+C copies Markdown (+ HTML), **Ctrl+Shift+C plain text**, Ctrl+X, Ctrl+V pastes one line as text or several lines as blocks (the first joins the caret's line, the last takes what followed the caret, list indents relative to the line pasted into). The page has "Copy" (Markdown) and "Copy as text" for all of it.
- Undo/redo (Ctrl+Z, Ctrl+Y or Ctrl+Shift+Z): snapshots of the blocks and the caret, typing grouped per word (a space, a move or a 2 s pause starts a new step), 300 steps.
- AltGr (Ctrl+Alt) is never taken as a shortcut, so `@`, `€`, `{` on German keyboards type as characters.

### Drawing (`internal/ui/noteseditor.go`, `styled_windows.go`)

- One DirectWrite layout per block with weight, italics, strikethrough and colour per range (`StyledParagraph`, `StyledLines`); the caret stops come from the same styled layout, so a caret after bold text sits where the text ends (tested with real metrics).
- H2 17 px and H3 14.5 px semibold, body 13 px; bullets "•", "◦", "▪" by level; numbers right-aligned; Tabler `square`/`square-check` boxes; a done task struck through and muted; emphasis markers faint; a mention as a light chip: "@flit" semibold, its time faint, its brackets taking room but no ink.
- Space: 18 px above an H2, 12 above an H3, 6 after a heading, 10 between paragraphs, 3 between list items.
- The caret is steady (no blinking: nothing repaints while nothing changes) and kept in view by scrolling as little as needed (`Ctx.KeepInView`); a toolbar above the text (H2, H3, bullets, numbers, task, bold, italic, @flit) acts like the shortcuts.
- The window routes keys and the pointer to the focused editor (`Ctx.Editor`, `host.focus`): a click focuses it; its toolbar and boxes keep (or give) it the keyboard; a click elsewhere, Esc, or a text field starting to edit takes it away. Characters come from WM_CHAR (surrogate pairs joined), keys from WM_KEYDOWN; the control characters Windows also sends for Enter, Tab and Backspace are ignored, so nothing acts twice.

### Where it lives (`internal/app/notes_windows.go`)

- The recording's page: a **Notes** section under its actions (during and after the recording), with "Copy" and "Copy as text".
- Opened ready to type, caret at the end, from the notch's **Notes** button while recording, Home's **Notes** button, and a click on Home's recording card.
- Each recording's editor stays in memory while flit runs (undo and caret survive going elsewhere); saved a second after the last change and at once on leaving the page, hiding the window, the end of the recording and quitting; empty notes leave no files; deleting a recording forgets them. The log gets the line count, never the text.

### Tests

- `internal/notes`: parsing variants, Markdown output and round trips, numbering, plain text, HTML, inline styles and mentions; every editing rule above (shortcuts, Enter/Backspace in lists, indent limits, tasks, mentions with and without a recording, typing times, undo grouping, selection, copy, paste, emphasis, word moves, kinds).
- `internal/session`: notes saved and read back with their times; times kept for unchanged lines after an outside edit; empty notes remove the files.
- `internal/ui`: the widget with fake metrics (typing, Up/Down, AltGr, clicks, boxes, placeholder, focus request, keeping the caret in view), a picture with DirectWrite (`FLIT_RENDER_DIR`), bold wider than regular, the clipboard's HTML offsets, and **a real window driven by Windows messages** (click to focus, characters, Enter/Tab/Backspace with their control characters, surrogate pairs, toolbar, Esc, click elsewhere, nothing typed without focus).
- `internal/app`: from the notch's Notes button during a recording, typing through the page's editor gives the expected `notes.md`, each line timed about 12:40 into the meeting, the mention with its time; Home's card and button open the page; notes typed afterwards have no time; leaving the page saves; deleting forgets.

### Not yet

- IME composition (East Asian input), and a screen reader's view of the text (flit has no UI Automation provider anywhere yet).
- HTML on paste (from browsers and Office: headings and lists are kept only when they come as Markdown or plain text with markers).
- Find inside the notes; links and code formatting.
