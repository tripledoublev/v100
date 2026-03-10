# v100 Phase 400: Arts & Entertainment

Phase 400 focuses on the **Subjective Experience** of agentic research. It transforms v100 from a sterile diagnostic tool into an expressive, multimodal harness that celebrates the "Ghost in the Machine" through audio, ASCII art, and stylized UI.

---

## 1. The Sonic Harness (Multimodal Feedback)
**Objective:** Provide real-time audio cues for agent behavior to reduce cognitive load and add personality.
*   **Mechanism:** Integrate `mpv` or `ffplay` to play short, non-blocking sound effects for key events:
    *   `run.start`: Deep synth pad.
    *   `tool.call`: Mechanical click.
    *   `tool.error`: Distorted glitch sound.
    *   `run.end`: Success chime or failure minor-chord.
*   **Research Value:** Explores "Sonic Debugging"—identifying loops or thrashing through rhythmic patterns before reading logs.

## 2. Mascot Personalities & "Reactive Face"
**Objective:** Humanize the agent loop with a more expressive mascot system.
*   **Mechanism:** Expand `internal/ui/mascot.go` into a multi-character system:
    *   `v100-classic`: The current robot.
    *   `glitch-ghost`: Appears during high-hallucination/low-confidence runs.
    *   `monolith`: A minimalist block for heavy reasoning turns.
*   **Mechanism:** Move the mascot from the welcome screen to a permanent corner of the TUI (Status Pane), reacting in real-time to every `model.token`.

## 3. The "Studio" Dashboard (Enhanced Radio)
**Objective:** Turn the background radio from a hidden feature into a core aesthetic pillar.
*   **Mechanism:** Create a dedicated "Studio View" (Ctrl+R) featuring:
    *   Full-width color-gradient ASCII waveforms.
    *   "Vinyl" metadata frames for current tracks.
    *   Integration with local music folders for "Lo-fi Study Beats" during long-horizon runs.

## 4. Generative Art Tools (The Creative Palette)
**Objective:** Enable agents to express state through visual media.
*   **Mechanism:** Add `internal/tools/dynamic/creative.go`:
    *   `pixel_canvas`: A tool for the agent to "paint" state as a BMP/PNG.
    *   `synth_voice`: Allows the agent to "speak" its reasoning via TTS (e.g., `espeak` or `say`).
*   **Research Value:** Studies the agent's ability to communicate non-symbolically.

---
*Document produced by v100 Research Harness - Arts & Strategic Spec v0.4.0*
