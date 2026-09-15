---
id: files
title: Files
summary: Browse, edit, upload, download, and manage files in the project directory.
---
# Files

Full filesystem access scoped to the project's working directory and
everything under it — the runtime enforces this containment independently
of the browser, so a crafted path can't escape the project root.

- **New file / New folder / Upload** create content in the current
  directory; drag-and-drop upload is supported.
- Row actions cover Open, Edit, Download, Rename, Copy, Move, Properties,
  and Delete.
- Select multiple rows for bulk **Delete / Download / Copy to… / Move to…**.
- The search box filters the current listing by name.

Opening a text file loads it in the built-in editor (see
[Code](/help.html#code)) rather than downloading it.
