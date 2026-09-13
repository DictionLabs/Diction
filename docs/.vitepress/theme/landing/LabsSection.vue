<script setup lang="ts">
import { ref } from 'vue'

// Diction Labs: what is under the keys. The illustration is a CSS 3D exploded
// stack of five layers; the tiles on the right are the four things worth saying.

interface Layer {
  id: string
  label: string
}

// DOM order is top-first (what a phone list should read); the stack puts the
// last one at the bottom and raises them bottom-first.
const layers: Layer[] = [
  { id: 'keys', label: 'Keys' },
  { id: 'ac', label: 'Autocorrect engine' },
  { id: 'stt', label: 'Speech models' },
  { id: 'wt', label: 'Writing Tools' },
  { id: 'enc', label: 'Encryption' },
]

// Top layer: a key grid drawn with flex boxes. f = key width in units.
const u = (n: number, c = '') => Array.from({ length: n }, () => ({ f: 1, c }))
const keyRows = [
  u(10),
  u(9),
  [{ f: 1.5, c: 'dim' }, ...u(7), { f: 1.5, c: 'dim' }],
  [
    { f: 1.5, c: 'dim' },
    { f: 1.2, c: 'dim' },
    { f: 1.2, c: 'mic' },
    { f: 4.6, c: '' },
    { f: 2.2, c: 'dim' },
  ],
]

// Speech models: a static waveform (heights in tenths of an em).
const wave = [6, 10, 18, 26, 34, 22, 40, 30, 16, 24, 38, 44, 28, 14, 20, 36, 26, 12, 18, 30, 42, 24, 10, 16, 22, 8]

// Autocorrect: a deterministic weight matrix under the candidate strip.
const cells = Array.from({ length: 36 }, (_, i) => 0.1 + (((i * 7) % 11) / 11) * 0.45)

// Writing Tools: text lines as bars; one struck and replaced, two list items.
const lines = [
  [{ w: '88%' }],
  [{ w: '22%', c: 'strike' }, { w: '38%', c: 'on' }],
  [{ c: 'num' }, { w: '52%' }],
  [{ c: 'num' }, { w: '44%' }],
  [{ w: '68%' }],
]

interface Tile {
  layer: string
  label: string
  body: string
  href?: string
  cta?: string
}

const tiles: Tile[] = [
  {
    layer: 'ac',
    label: 'In-house autocorrect engine',
    body: 'A C++ correction engine with per-language word statistics, tuned on our own benchmarks.',
  },
  {
    layer: 'stt',
    label: 'Models we convert ourselves',
    body: 'Speech models converted and tuned for iPhone and servers, published on Hugging Face.',
    href: 'https://huggingface.co/DictionLabs',
    cta: 'huggingface.co/DictionLabs',
  },
  {
    layer: 'enc',
    label: 'Encrypted transcripts',
    body: 'AES-256-GCM on every transcript, keys exchanged with X25519, fresh per request.',
  },
  {
    layer: 'enc',
    label: 'Key rotation',
    body: 'Self-hosted pairing keys rotate; the gateway prints a new QR whenever you ask.',
  },
]

// Hovering a tile lights the matching layer. Decorative only.
const hl = ref<string | null>(null)

const pad = (n: number) => String(n).padStart(2, '0')
</script>

<template>
  <section class="ld-section ink ld-grid-bg labs">
    <div class="ld-container">
      <div class="ld-label-row" v-reveal>
        <span class="ld-mono">05 / Diction Labs</span>
        <span class="ld-mono">Research, models, measurement</span>
      </div>

      <div class="labs-head" v-reveal="{ delay: 60 }">
        <h2 class="ld-h2">An engineering project, not a wrapper.</h2>
        <p class="ld-lead">Here is what sits under the keys.</p>
      </div>

      <div class="labs-grid">
        <div
          class="labs-fig"
          v-reveal="{ delay: 80 }"
          role="img"
          aria-label="The Diction keyboard in five layers: keys, autocorrect engine, speech models, Writing Tools, encryption."
        >
          <div class="labs-stage">
            <div class="labs-3d">
              <div
                v-for="(l, i) in layers"
                :key="l.id"
                class="labs-layer"
                :class="[`layer-${l.id}`, { 'is-hl': hl === l.id }]"
                :style="{ '--i': layers.length - 1 - i }"
              >
                <div class="labs-under"></div>

                <div class="labs-face">
                  <!-- Keys -->
                  <div v-if="l.id === 'keys'" class="labs-motif keys">
                    <div class="kb-strip"><i></i><i class="on"></i><i></i></div>
                    <div v-for="(row, r) in keyRows" :key="r" class="kb-row">
                      <i v-for="(k, j) in row" :key="j" class="kb-key" :class="k.c" :style="{ flex: k.f }"></i>
                    </div>
                  </div>

                  <!-- Autocorrect engine -->
                  <div v-else-if="l.id === 'ac'" class="labs-motif ac">
                    <div class="ac-strip"><i></i><i class="on"></i><i></i></div>
                    <div class="ac-matrix">
                      <i v-for="(o, c) in cells" :key="c" :style="{ opacity: o }"></i>
                    </div>
                  </div>

                  <!-- Speech models -->
                  <div v-else-if="l.id === 'stt'" class="labs-motif wave">
                    <i v-for="(h, w) in wave" :key="w" :class="{ on: w % 9 === 4 }" :style="{ height: h / 10 + 'em' }"></i>
                  </div>

                  <!-- Writing Tools -->
                  <div v-else-if="l.id === 'wt'" class="labs-motif wt">
                    <div v-for="(row, r) in lines" :key="r" class="wt-row">
                      <i v-for="(b, j) in row" :key="j" :class="b.c" :style="b.w ? { width: b.w } : undefined"></i>
                    </div>
                  </div>

                  <!-- Encryption -->
                  <div v-else class="labs-motif enc">
                    <div class="lock"><i class="shackle"></i><i class="body"></i></div>
                  </div>
                </div>

                <div class="labs-tag">
                  <i class="dot"></i>
                  <i class="line"></i>
                  <span class="idx">{{ pad(i + 1) }}</span>
                  <span class="txt">{{ l.label }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="labs-tiles ld-stagger" v-reveal="{ delay: 120 }">
          <component
            :is="t.href ? 'a' : 'div'"
            v-for="t in tiles"
            :key="t.label"
            class="labs-tile"
            :href="t.href"
            :target="t.href ? '_blank' : undefined"
            :rel="t.href ? 'noopener' : undefined"
            @mouseenter="hl = t.layer"
            @mouseleave="hl = null"
            @focusin="hl = t.layer"
            @focusout="hl = null"
          >
            <span class="ld-mono labs-tile-label">{{ t.label }}</span>
            <p class="labs-tile-body">{{ t.body }}</p>
            <span v-if="t.cta" class="ld-mono labs-tile-cta">{{ t.cta }}</span>
          </component>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.labs {
  background-color: #0b0b0d;
  --labs-accent: #4da3ff;
  --labs-line: rgba(255, 255, 255, 0.18);
}
/* Blue is transcription, violet is Writing Tools. The other layers stay neutral. */
.layer-stt {
  --layer-accent: var(--labs-accent);
}
.layer-wt {
  --layer-accent: var(--ld-violet);
}

.labs-head {
  display: grid;
  grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr);
  gap: 1rem 4rem;
  align-items: end;
  margin-bottom: clamp(2.5rem, 5vw, 4rem);
}
.labs-head .ld-lead {
  max-width: 420px;
  justify-self: end;
  text-align: right;
}

.labs-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr);
  gap: 3rem;
  align-items: center;
}

/* ---------- Exploded stack ---------- */
/* Everything inside the stage is sized in em with 1em = 10px at full size.
   The container query below shrinks the whole drawing to the column width. */
.labs-fig {
  container-type: inline-size;
  min-width: 0;
}
.labs-stage {
  font-size: 10px;
  font-size: min(10px, calc(100cqw / 60));
  position: relative;
  width: 60em;
  height: 45em;
  perspective: 200em;
}
.labs-3d {
  position: absolute;
  left: 4.6em;
  top: 25.3em;
  width: 28em;
  height: 18em;
  transform-style: preserve-3d;
  transform: rotateX(55deg) rotateZ(-30deg);
}
.labs-layer {
  --z: calc(var(--i) * 7.2em);
  position: absolute;
  inset: 0;
  transform-style: preserve-3d;
  transform: translateZ(calc(var(--z) - 6em));
  opacity: 0;
  transition:
    transform 0.9s var(--ld-ease),
    opacity 0.5s linear;
  transition-delay: calc(var(--i) * 110ms);
}
.labs-fig.is-visible .labs-layer {
  transform: translateZ(var(--z));
  opacity: 1;
}

.labs-under {
  position: absolute;
  inset: 0;
  border-radius: 1.4em;
  background: #07080a;
  transform: translateZ(-0.6em);
}
.labs-face {
  position: absolute;
  inset: 0;
  border-radius: 1.4em;
  background: rgba(21, 22, 26, 0.94);
  border: 1px solid var(--labs-line);
  overflow: hidden;
  transition: border-color 0.25s, background-color 0.25s;
}
.layer-keys .labs-face {
  background: rgba(27, 28, 33, 0.97);
}
.layer-stt .labs-face,
.layer-wt .labs-face {
  border-color: color-mix(in srgb, var(--layer-accent) 70%, transparent);
}
.labs-layer.is-hl .labs-face {
  border-color: var(--layer-accent, var(--labs-accent));
}

/* Tag: billboarded back to the screen, anchored at the rightmost corner. */
.labs-tag {
  position: absolute;
  left: 100%;
  top: 100%;
  transform-origin: 0 0;
  transform: rotateZ(30deg) rotateX(-55deg) translate(-0.4em, -50%);
  display: flex;
  align-items: center;
  white-space: nowrap;
  font-family: var(--vp-font-family-mono);
  font-size: 1.2em;
  font-weight: 500;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: rgba(255, 255, 255, 0.82);
  transition: color 0.25s;
}
.labs-tag .dot {
  width: 0.66em;
  height: 0.66em;
  border-radius: 50%;
  background: #fff;
  flex: none;
  transition: background-color 0.25s;
}
.labs-tag .line {
  width: 3.4em;
  height: 1px;
  background: rgba(255, 255, 255, 0.32);
  flex: none;
  margin-right: 0.8em;
}
.labs-tag .idx {
  color: rgba(255, 255, 255, 0.38);
  margin-right: 0.7em;
}
.layer-stt .labs-tag,
.layer-wt .labs-tag,
.labs-layer.is-hl .labs-tag {
  color: var(--layer-accent, var(--labs-accent));
}
.layer-stt .labs-tag .dot,
.layer-wt .labs-tag .dot,
.labs-layer.is-hl .labs-tag .dot {
  background: var(--layer-accent, var(--labs-accent));
}
.layer-stt .labs-tag .line,
.layer-wt .labs-tag .line {
  background: color-mix(in srgb, var(--layer-accent) 55%, transparent);
}

/* Motifs, drawn in the plane of each layer. Lower layers are partly covered by
   the one above, so their motif sits in the bottom half of the face. */
.labs-motif i {
  display: block;
}

/* Keys */
.keys {
  position: absolute;
  inset: 1.4em;
  display: flex;
  flex-direction: column;
  gap: 0.55em;
}
.kb-strip {
  display: flex;
  gap: 0.8em;
  padding: 0 4em 0.3em;
}
.kb-strip i {
  flex: 1;
  height: 1.5em;
  border-radius: 0.75em;
  background: rgba(255, 255, 255, 0.08);
}
.kb-strip i.on {
  background: rgba(77, 163, 255, 0.4);
}
.kb-row {
  display: flex;
  gap: 0.45em;
}
.kb-row:nth-child(3) {
  padding-inline: 5%;
}
.kb-key {
  height: 2.5em;
  border-radius: 0.5em;
  background: rgba(255, 255, 255, 0.17);
}
.kb-key.dim {
  background: rgba(255, 255, 255, 0.08);
}
.kb-key.mic {
  background: var(--labs-accent);
}

/* Autocorrect engine */
.ac {
  position: absolute;
  left: 1.6em;
  right: 1.6em;
  bottom: 1.8em;
  display: flex;
  flex-direction: column;
  gap: 0.9em;
}
.ac-strip {
  display: flex;
  gap: 0.8em;
  padding: 0 3em;
}
.ac-strip i {
  flex: 1;
  height: 1.6em;
  border-radius: 0.8em;
  border: 1px solid rgba(255, 255, 255, 0.22);
}
.ac-strip i.on {
  border-color: transparent;
  background: var(--labs-accent);
}
.ac-matrix {
  display: grid;
  grid-template-columns: repeat(12, 1fr);
  gap: 0.4em;
}
.ac-matrix i {
  height: 0.8em;
  border-radius: 0.2em;
  background: #fff;
}

/* Speech models */
.wave {
  position: absolute;
  left: 1.8em;
  right: 1.8em;
  bottom: 2.2em;
  height: 5em;
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.wave i {
  width: 0.4em;
  border-radius: 0.2em;
  background: rgba(255, 255, 255, 0.38);
}
.wave i.on {
  background: var(--labs-accent);
}

/* Writing Tools */
.wt {
  position: absolute;
  left: 1.8em;
  right: 1.8em;
  bottom: 2em;
  display: flex;
  flex-direction: column;
  gap: 0.85em;
}
.wt-row {
  display: flex;
  align-items: center;
  gap: 0.6em;
  padding-left: 0;
}
.wt-row i {
  height: 0.6em;
  border-radius: 0.3em;
  background: rgba(255, 255, 255, 0.28);
}
.wt-row i.on {
  background: var(--ld-violet);
}
.wt-row i.strike {
  position: relative;
  background: rgba(255, 255, 255, 0.12);
}
.wt-row i.strike::after {
  content: '';
  position: absolute;
  left: -0.2em;
  right: -0.2em;
  top: 50%;
  height: 1px;
  background: rgba(255, 255, 255, 0.6);
}
.wt-row i.num {
  width: 0.6em;
  background: rgba(255, 255, 255, 0.55);
}

/* Encryption */
.layer-enc .labs-face {
  background-image: repeating-linear-gradient(
    -45deg,
    rgba(255, 255, 255, 0.07) 0 1px,
    transparent 1px 9px
  );
}
.enc {
  position: absolute;
  inset: 0;
}
.lock {
  position: absolute;
  left: 50%;
  bottom: 2.6em;
  transform: translateX(-50%);
  display: flex;
  flex-direction: column;
  align-items: center;
}
.lock .shackle {
  width: 1.6em;
  height: 1.2em;
  border: 0.22em solid rgba(255, 255, 255, 0.8);
  border-bottom: none;
  border-radius: 0.8em 0.8em 0 0;
}
.lock .body {
  width: 2.6em;
  height: 1.9em;
  border-radius: 0.35em;
  background: rgba(255, 255, 255, 0.85);
}

/* ---------- Tiles ---------- */
.labs-tiles {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1px;
  background: rgba(255, 255, 255, 0.12);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 16px;
  overflow: hidden;
}
.labs-tile {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  padding: 1.4rem 1.4rem 1.5rem;
  background: #0b0b0d;
  color: inherit;
  text-decoration: none;
  transition: background-color 0.25s;
}
.labs-tile:hover,
.labs-tile:focus-visible {
  background: #131417;
  outline: none;
}
.labs-tile-label {
  color: #6db3ff;
}
.labs-tile-body {
  color: rgba(255, 255, 255, 0.72);
  font-size: 0.95rem;
  line-height: 1.5;
  flex: 1;
  text-wrap: pretty;
}
.labs-tile-cta {
  color: rgba(255, 255, 255, 0.45);
  text-transform: none;
  letter-spacing: 0.02em;
  overflow-wrap: anywhere;
}
.labs-tile:hover .labs-tile-cta {
  color: #6db3ff;
}

/* ---------- Breakpoints ---------- */
@media (max-width: 960px) {
  .labs-head,
  .labs-grid {
    grid-template-columns: minmax(0, 1fr);
  }
  .labs-head .ld-lead {
    justify-self: start;
    text-align: left;
  }
  .labs-fig {
    width: 100%;
    max-width: 600px;
    margin-inline: auto;
  }
}

/* Phone: the same five layers as a flat stacked list, no 3D. */
@media (max-width: 640px) {
  .labs-stage {
    font-size: 10px;
    width: auto;
    height: auto;
    perspective: none;
  }
  .labs-3d {
    position: static;
    width: auto;
    height: auto;
    transform: none;
    transform-style: flat;
    display: grid;
    gap: 8px;
  }
  .labs-layer {
    position: relative;
    inset: auto;
    height: 56px;
    transform: translateY(12px);
    transform-style: flat;
    transition-duration: 0.6s;
    transition-delay: calc((4 - var(--i)) * 70ms);
  }
  .labs-fig.is-visible .labs-layer {
    transform: none;
  }
  .labs-under,
  .labs-motif {
    display: none;
  }
  .labs-face {
    border-radius: 12px;
  }
  .labs-tag {
    left: 16px;
    top: 50%;
    transform: translateY(-50%);
    font-size: 12px;
  }
  .labs-tag .line {
    display: none;
  }
  .labs-tag .dot {
    margin-right: 12px;
  }
  .labs-tiles {
    grid-template-columns: minmax(0, 1fr);
  }
  .labs-tile {
    padding: 1.2rem 1.15rem 1.3rem;
  }
}

@media (prefers-reduced-motion: reduce) {
  .labs-layer {
    transition: none;
    transform: translateZ(var(--z));
    opacity: 1;
  }
  .labs-face,
  .labs-tag,
  .labs-tag .dot,
  .labs-tile {
    transition: none;
  }
  @media (max-width: 640px) {
    .labs-layer {
      transform: none;
    }
  }
}
</style>
