<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'

// Optional real terminal recording. When set, the <video> replaces the
// scripted animation inside the terminal body (e.g. video="/terminal.mp4").
const props = defineProps<{ video?: string }>()

// Mirrors docs/features/self-hosting-setup.md ("Pick one and start" and
// "Connecting the app"). Output shape follows real `docker compose up -d`.
const CMD_UP = 'docker compose --profile parakeet up -d'
const UP_LINES = [
  '[+] Running 3/3',
  ' ✔ Network diction_default        Created',
  ' ✔ Container diction-parakeet     Started',
  ' ✔ Container diction-gateway      Started',
]
const CMD_AUTH = 'docker compose exec gateway gateway auth'
const SCAN_LINE = 'Scan with Diction: Self-Hosted > Scan to pair'

// Deterministic 25x25 grid shaped like a version 2 QR code: three finder
// patterns with separators, timing lines, an alignment pattern and the dark
// module, with pseudo-random data elsewhere. Pure function of a fixed seed,
// so SSR and client render the same cells. It is not a scannable code.
const QR_SIZE = 25
function mulberry32(seed: number) {
  let a = seed
  return () => {
    a |= 0
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}
function finderAt(x: number, y: number, ox: number, oy: number): boolean | null {
  const lx = x - ox
  const ly = y - oy
  // 7x7 finder plus a one-module white separator ring around it
  if (lx < -1 || lx > 7 || ly < -1 || ly > 7) return null
  if (lx === -1 || lx === 7 || ly === -1 || ly === 7) return false
  if (lx === 0 || lx === 6 || ly === 0 || ly === 6) return true
  if (lx >= 2 && lx <= 4 && ly >= 2 && ly <= 4) return true
  return false
}
function alignmentAt(x: number, y: number): boolean | null {
  // version 2 alignment pattern centered at (18, 18)
  const lx = x - 16
  const ly = y - 16
  if (lx < 0 || lx > 4 || ly < 0 || ly > 4) return null
  if (lx === 0 || lx === 4 || ly === 0 || ly === 4) return true
  if (lx === 2 && ly === 2) return true
  return false
}
function buildQr(): boolean[] {
  const rand = mulberry32(4211)
  const cells: boolean[] = []
  for (let y = 0; y < QR_SIZE; y++) {
    for (let x = 0; x < QR_SIZE; x++) {
      const f =
        finderAt(x, y, 0, 0) ??
        finderAt(x, y, QR_SIZE - 7, 0) ??
        finderAt(x, y, 0, QR_SIZE - 7) ??
        alignmentAt(x, y)
      if (f !== null) {
        cells.push(f)
      } else if (y === 6 && x >= 8 && x <= QR_SIZE - 9) {
        cells.push(x % 2 === 0) // horizontal timing pattern
      } else if (x === 6 && y >= 8 && y <= QR_SIZE - 9) {
        cells.push(y % 2 === 0) // vertical timing pattern
      } else if (x === 8 && y === QR_SIZE - 8) {
        cells.push(true) // dark module
      } else {
        cells.push(rand() > 0.52)
      }
    }
  }
  return cells
}
const qrCells = buildQr()

const terminalEl = ref<HTMLElement | null>(null)
const videoEl = ref<HTMLVideoElement | null>(null)

// 0: typing up, 1: up output, 2: typing auth, 3: auth output, 4: done
const typedUp = ref('')
const upLines = ref<string[]>([])
const authStarted = ref(false)
const typedAuth = ref('')
const qrVisible = ref(false)
const scanVisible = ref(false)
const playing = ref(false)
const finished = ref(false)

let disposed = false
let reduceMotion = false
const timers: number[] = []

function sleep(ms: number) {
  return new Promise<void>((resolve) => {
    const id = window.setTimeout(resolve, ms)
    timers.push(id)
  })
}

function resetState() {
  typedUp.value = ''
  upLines.value = []
  authStarted.value = false
  typedAuth.value = ''
  qrVisible.value = false
  scanVisible.value = false
  finished.value = false
}

function showFinalState() {
  typedUp.value = CMD_UP
  upLines.value = [...UP_LINES]
  authStarted.value = true
  typedAuth.value = CMD_AUTH
  qrVisible.value = true
  scanVisible.value = true
  finished.value = true
  playing.value = false
}

async function typeInto(target: typeof typedUp, text: string) {
  for (let i = 1; i <= text.length; i++) {
    if (disposed) return
    target.value = text.slice(0, i)
    await sleep(22)
  }
}

async function playSequence() {
  if (disposed) return
  resetState()
  playing.value = true

  await typeInto(typedUp, CMD_UP)
  await sleep(420)
  for (const line of UP_LINES) {
    if (disposed) return
    upLines.value = [...upLines.value, line]
    await sleep(line.startsWith('[+]') ? 260 : 380)
  }

  await sleep(500)
  if (disposed) return
  authStarted.value = true
  await sleep(200)
  await typeInto(typedAuth, CMD_AUTH)
  await sleep(380)
  if (disposed) return
  qrVisible.value = true
  await sleep(320)
  if (disposed) return
  scanVisible.value = true
  playing.value = false
  finished.value = true
}

function replay() {
  if (props.video) {
    const v = videoEl.value
    if (v) {
      v.currentTime = 0
      v.play().catch(() => {})
    }
    return
  }
  if (playing.value) return
  if (reduceMotion) {
    showFinalState()
    return
  }
  playSequence()
}

let observer: IntersectionObserver | null = null

onMounted(() => {
  if (props.video) return
  reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  if (reduceMotion) {
    showFinalState()
    return
  }
  if (typeof IntersectionObserver === 'undefined' || !terminalEl.value) {
    playSequence()
    return
  }
  observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting && !playing.value && !finished.value) {
          playSequence()
          if (terminalEl.value) observer?.unobserve(terminalEl.value)
        }
      }
    },
    { threshold: 0.35 },
  )
  observer.observe(terminalEl.value)
})

onUnmounted(() => {
  disposed = true
  timers.forEach((id) => window.clearTimeout(id))
  observer?.disconnect()
})
</script>

<template>
  <section class="ld-section soft selfhost-section">
    <div class="ld-container">
      <div class="ld-label-row" v-reveal>
        <span class="ld-mono ld-accent ld-violet">07 / Self-host</span>
        <span class="ld-mono">diction.one</span>
      </div>

      <div class="split">
        <div class="col-text" v-reveal>
          <h2 class="ld-h2">Already run a speech model or an LLM at home? Plug it into your keyboard.</h2>
          <p class="ld-lead">One Docker command, scan the QR, done. Free, no account, no limits.</p>
          <div class="ld-actions">
            <a class="ld-btn brand" href="/features/self-hosting-setup">Self-hosting guide</a>
            <a class="ld-btn alt" href="https://github.com/DictionLabs/Diction" target="_blank" rel="noopener">
              <img src="/github-mark.svg" alt="" />
              Source
            </a>
          </div>
        </div>

        <div class="col-terminal" v-reveal="{ delay: 120 }">
          <div ref="terminalEl" class="terminal">
            <div class="terminal-bar">
              <span class="dot red" />
              <span class="dot yellow" />
              <span class="dot green" />
              <span class="terminal-title">ondrej@home-server: ~/diction</span>
              <button type="button" class="replay-btn" :disabled="playing" @click="replay">Replay</button>
            </div>

            <div v-if="video" class="terminal-body video">
              <video ref="videoEl" :src="video" autoplay muted loop playsinline />
            </div>

            <div v-else class="terminal-body">
              <div class="term-line prompt">
                <span class="prompt-sign">$</span>
                <span class="term-command">{{ typedUp }}</span>
                <span v-if="!authStarted && !finished" class="term-caret" aria-hidden="true" />
              </div>
              <div v-for="(line, i) in upLines" :key="i" class="term-line output" :class="{ ok: line.startsWith(' ✔') }">{{ line }}</div>

              <div v-if="authStarted" class="term-line prompt second">
                <span class="prompt-sign">$</span>
                <span class="term-command">{{ typedAuth }}</span>
                <span v-if="!finished" class="term-caret" aria-hidden="true" />
              </div>

              <div v-if="qrVisible" class="qr-wrap">
                <div class="qr-tile" role="img" aria-label="Illustrative pairing QR code">
                  <div class="qr-grid">
                    <span v-for="(on, i) in qrCells" :key="i" class="qr-cell" :class="{ on }" />
                  </div>
                </div>
                <span class="ld-mono qr-caption">Illustrative</span>
              </div>

              <div v-if="scanVisible" class="term-line scan">{{ SCAN_LINE }}</div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.split {
  display: grid;
  grid-template-columns: minmax(0, 0.9fr) minmax(0, 1.1fr);
  gap: clamp(2rem, 5vw, 4rem);
  align-items: center;
}

.col-text .ld-h2 {
  margin-top: 0.25rem;
}
.col-text .ld-lead {
  margin-top: 1.1rem;
  max-width: 480px;
}
.col-text .ld-actions {
  margin-top: 2rem;
}

.terminal {
  background: var(--ld-navy-950);
  border: 1px solid var(--ld-navy-700);
  color: var(--ld-navy-100);
  border-radius: 14px;
  overflow: hidden;
  box-shadow: var(--ld-shadow-md);
}

.terminal-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--ld-navy-700);
}
.dot {
  width: 11px;
  height: 11px;
  border-radius: 50%;
  display: inline-block;
}
.dot.red {
  background: #ff5f56;
}
.dot.yellow {
  background: #ffbd2e;
}
.dot.green {
  background: #27c93f;
}
.terminal-title {
  margin-left: 8px;
  font-size: 0.75rem;
  color: #8a8f98;
  font-family: var(--vp-font-family-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.replay-btn {
  margin-left: auto;
  flex: 0 0 auto;
  min-height: 30px;
  padding: 0 12px;
  border-radius: var(--ld-r-pill);
  border: 1px solid #363941;
  background: transparent;
  color: #9aa0a9;
  font-family: var(--vp-font-family-mono);
  font-size: 0.6875rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  cursor: pointer;
  transition: border-color 0.2s var(--ld-ease), color 0.2s var(--ld-ease);
}
.replay-btn:hover:not(:disabled) {
  border-color: #4d515c;
  color: #d7dadf;
}
.replay-btn:disabled {
  opacity: 0.5;
  cursor: default;
}

.terminal-body {
  padding: 18px 20px 22px;
  font-family: var(--vp-font-family-mono);
  font-size: 0.8125rem;
  line-height: 1.6;
  min-height: 460px;
}
.terminal-body.video {
  padding: 0;
  min-height: 0;
  aspect-ratio: 16 / 10;
}
.terminal-body.video video {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.term-line {
  white-space: pre;
  overflow-x: auto;
  scrollbar-width: none;
}
.term-line::-webkit-scrollbar {
  display: none;
}
.term-line.prompt {
  color: #e7e9ec;
  display: flex;
  gap: 8px;
  white-space: pre-wrap;
  word-break: break-word;
}
.term-line.prompt.second {
  margin-top: 14px;
}
.prompt-sign {
  color: #5fd0ff;
}
.term-caret {
  width: 7px;
  height: 1.05em;
  background: #d7dadf;
  display: inline-block;
  transform: translateY(2px);
  animation: term-blink 1s step-end infinite;
}
.term-line.output {
  color: #9aa0a9;
}
.term-line.output.ok {
  color: #4ddd7a;
}
.term-line.scan {
  color: #e7e9ec;
  margin-top: 4px;
}

.qr-wrap {
  margin: 12px 0 10px;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
}
.qr-tile {
  --qr-cell: 7px;
  background: #fff;
  padding: calc(var(--qr-cell) * 3);
  border-radius: 4px;
}
.qr-grid {
  display: grid;
  grid-template-columns: repeat(25, var(--qr-cell));
  grid-template-rows: repeat(25, var(--qr-cell));
  width: max-content;
}
.qr-cell {
  width: var(--qr-cell);
  height: var(--qr-cell);
  background: transparent;
}
.qr-cell.on {
  background: var(--ld-navy-950);
}
.qr-caption {
  color: rgba(255, 255, 255, 0.4);
}

@keyframes term-blink {
  50% {
    opacity: 0;
  }
}

@media (max-width: 860px) {
  .split {
    grid-template-columns: minmax(0, 1fr);
  }
  .split > * {
    min-width: 0;
    max-width: 100%;
  }
  .col-text .ld-lead {
    max-width: none;
  }
}

@media (max-width: 640px) {
  .terminal-body {
    font-size: 11.5px;
    padding: 14px 14px 18px;
    min-height: 380px;
  }
  .qr-tile {
    --qr-cell: 6px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .term-caret {
    animation: none !important;
    opacity: 0;
  }
}
</style>
