---
id: code
title: Code
summary: A multi-file, multi-tab code editor for the project directory, with CodeMirror syntax highlighting for ~180 languages.
---
# Code

The Code tab is a dedicated editor, separate from the one-off file editor
opened from [Files](/help.html#files): a file tree on the left, multiple
open tabs, and CodeMirror-based syntax highlighting auto-detected from each
file's name, covering close to 180 languages.

- Click a file in the tree to open it in a new tab.
- **Ctrl/Cmd+S** saves the active tab back to the project directory.
- Markdown files can be previewed as rendered HTML.
- The language picker lets you override syntax highlighting for any file,
  regardless of what was auto-detected (or nothing, if none was).

Auto-detection only tries languages enabled in your
[Account](/help.html#account) page's Code tab languages checklist —
`JavaScript, TypeScript, Go, Python, HTML, CSS, JSON, YAML, Markdown, SQL,
Shell` by default. A file in a disabled language still opens fine; just
pick it manually from the language picker.

Changes save straight to disk on the runtime — there's no separate
"publish" step.
