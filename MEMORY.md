# v100 Memory

## 2026-04-18
- UI image rendering split into two paths: live event rendering writes to terminal via `ImageRenderer`, transcript/replay paths render text summaries only
- Kitty-in-tmux now degrades to text instead of emitting raw escape sequences
- `fs_render_image` / `image.inline` flow still uses PNG payloads, but redraw-safe transcript output no longer re-emits terminal side effects
- ATProto updates in progress: digest URI dedup, graph explorer `actor` support, recall output strips embeddings and exposes `count`
