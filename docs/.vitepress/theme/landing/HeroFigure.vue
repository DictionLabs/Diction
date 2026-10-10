<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'

// Hero drawing: the product moment, as a loop.
//   idle   the bar shows the mic and the Diction wordmark, as in the app
//   listen spoken words appear on the left while the bar's meter moves
//   strike the fillers are struck; the meter settles
//   clean  the spoken text crossfades into the cleaned, formatted message,
//          which types into the Messages field; the bar goes back to idle
//   hold / fade, then again
// Reduced motion shows the finished state. Sized in em, 1em = 10px at full width.

interface Word {
  t: string
  cut?: boolean
}
const spoken: Word[] = 'um yeah so friday works for me, uh, I just need three things before then the slides the demo laptop and someone to take notes'
  .split(' ')
  .map(t => ({ t, cut: ['um', 'so', 'uh,'].includes(t) }))

const intro = 'Yeah, Friday works for me. I just need three things before then:'
const items = ['The slides', 'The demo laptop', 'Someone to take notes']
const message = `${intro}\n${items.map((s, i) => `${i + 1}. ${s}`).join('\n')}`

const u = (n: number, c = '') => Array.from({ length: n }, () => ({ f: 1, c }))
const keyRows = [
  u(10),
  u(9),
  [{ f: 1.4, c: 'dim' }, ...u(7), { f: 1.4, c: 'dim' }],
  [{ f: 2.4, c: 'dim' }, { f: 5.6, c: '' }, { f: 2.4, c: 'dim' }],
]

// Timeline state
type Phase = 'idle' | 'listen' | 'strike' | 'clean' | 'hold' | 'fade'
const phase = ref<Phase>('idle')
const shown = ref(0) // spoken words revealed
const typed = ref(0) // characters typed into the field

let timers: number[] = []
const at = (ms: number, fn: () => void) => timers.push(window.setTimeout(fn, ms))

function run() {
  timers.forEach(clearTimeout)
  timers = []
  phase.value = 'idle'
  shown.value = 0
  typed.value = 0
  let t = 1100
  at(t, () => (phase.value = 'listen'))
  spoken.forEach((_, i) => at((t += 210), () => (shown.value = i + 1)))
  at((t += 600), () => (phase.value = 'strike'))
  at((t += 1100), () => (phase.value = 'clean'))
  t += 400
  for (let c = 1; c <= message.length; c++) at((t += 18), () => (typed.value = c))
  at((t += 400), () => (phase.value = 'hold'))
  at((t += 3600), () => (phase.value = 'fade'))
  at((t += 700), run)
}

// Recording meter, after the app's WaveformView: 16 bars, an envelope that is
// tallest in the middle, each bar on its own wobble, all riding a slow "speech"
// level. Heights ease toward their target so starting and stopping are smooth.
const BARS = 16
const centre = (BARS - 1) / 2
const scales = Array.from({ length: BARS }, (_, i) => {
  const d = Math.abs(i - centre) / centre
  return Math.max(0.15, 1 - d * d * 0.85)
})
const seeds = Array.from({ length: BARS }, (_, i) => ({
  f: 5 + ((i * 7) % 11) * 0.9,
  p: (i * 2.39) % (Math.PI * 2),
}))
const levels = ref<number[]>(Array(BARS).fill(0))
let raf = 0
function tick(now: number) {
  const s = now / 1000
  const live = phase.value === 'listen'
  const speech = 0.55 + 0.45 * Math.sin(s * 2.1) * Math.sin(s * 0.7 + 1)
  levels.value = levels.value.map((v, i) => {
    const wobble = 0.35 + 0.65 * Math.abs(Math.sin(s * seeds[i].f + seeds[i].p))
    const target = live ? scales[i] * wobble * speech : 0
    return v + (target - v) * 0.18
  })
  raf = requestAnimationFrame(tick)
}

onMounted(() => {
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    phase.value = 'hold'
    shown.value = spoken.length
    typed.value = message.length
    return
  }
  raf = requestAnimationFrame(tick)
  run()
})
onUnmounted(() => {
  timers.forEach(clearTimeout)
  cancelAnimationFrame(raf)
})
</script>

<template>
  <div
    class="hm"
    role="img"
    :aria-label="`Someone says: ${spoken.map(w => w.t).join(' ')}. The Diction keyboard types: ${message}`"
  >
    <div class="hm-stage" :class="`is-${phase}`">
      <!-- Left: what was said, then what gets sent. Two layers, crossfaded. -->
      <div class="hm-left" aria-hidden="true">
        <p class="hm-spoken">
          <template v-for="(w, i) in spoken" :key="i">
            <span class="w" :class="{ on: i < shown, cut: w.cut }">{{ w.t }}</span>{{ ' ' }}
          </template>
        </p>
        <div class="hm-clean">
          <p>{{ intro }}</p>
          <ol>
            <li v-for="(s, i) in items" :key="i">{{ s }}</li>
          </ol>
        </div>
      </div>

      <!-- Right: the phone -->
      <div class="hm-phone">
        <div class="hm-screen">
          <div class="hm-status"><span>9:41</span><i class="island"></i><span class="bars"><i></i><i></i><i></i></span></div>

          <div class="hm-chat">
            <div class="hm-contact"><i class="avatar">A</i><span>Alex</span></div>
            <div class="hm-bubble">Can we push the review?</div>
          </div>

          <div class="hm-field">
            <span v-if="typed" class="text">{{ message.slice(0, typed) }}<i class="caret"></i></span>
            <span v-else class="placeholder"><i class="caret"></i>iMessage</span>
            <i class="send" :class="{ on: typed }"><i class="arrow"></i></i>
          </div>

          <div class="hm-kb">
            <i class="grabber"></i>
            <div class="hm-bar" :class="{ rec: phase === 'listen' || phase === 'strike' }">
              <!-- Idle: mic + wordmark -->
              <span class="layer idle">
                <i class="mic"></i>
                <span class="wordmark">Diction</span>
              </span>
              <!-- Recording: [ pause  |||meter|||  ✓ ] -->
              <span class="layer rec">
                <span class="pause"><i></i><i></i></span>
                <span class="meter">
                  <i v-for="(v, i) in levels" :key="i" :style="{ transform: `scaleY(${0.08 + 0.92 * v})` }"></i>
                </span>
                <span class="tick"><i></i></span>
              </span>
            </div>
            <div v-for="(row, r) in keyRows" :key="r" class="hm-row">
              <i v-for="(k, j) in row" :key="j" class="key" :class="k.c" :style="{ flex: k.f }"></i>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.hm {
  --hm-blue: #4da3ff;
  --hm-bar: #3b8cf0;
  --hm-violet: #c78cee;
  --hm-ease: cubic-bezier(0.22, 0.61, 0.36, 1);
  container-type: inline-size;
  position: absolute;
  inset: 0;
  overflow: hidden;
}
.hm-stage {
  font-size: min(10px, calc(100cqw / 115));
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7em;
}
.hm i {
  display: block;
  font-style: normal;
}

/* Left: two layers in one grid cell, so swapping them never moves anything */
.hm-left {
  width: 54em;
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  align-items: center;
}
.hm-spoken,
.hm-clean {
  grid-area: 1 / 1;
  margin: 0;
  font-size: 3.4em;
  line-height: 1.32;
  letter-spacing: -0.01em;
  transition:
    opacity 0.7s var(--hm-ease),
    transform 0.7s var(--hm-ease);
}

.hm-spoken {
  color: rgba(255, 255, 255, 0.55);
}
.hm-spoken .w {
  opacity: 0;
  transition:
    opacity 0.45s var(--hm-ease),
    color 0.5s ease,
    text-decoration-color 0.5s ease;
  text-decoration: line-through;
  text-decoration-color: transparent;
  text-decoration-thickness: 0.07em;
}
.hm-spoken .w.on {
  opacity: 1;
}
.is-strike .hm-spoken .w.cut {
  color: rgba(255, 255, 255, 0.25);
  text-decoration-color: var(--hm-violet);
}

.hm-clean {
  color: #fff;
  opacity: 0;
  transform: translateY(0.4em);
}
.hm-clean p {
  margin: 0 0 0.35em;
}
.hm-clean ol {
  margin: 0;
  padding-left: 1.3em;
  list-style: decimal;
}
.hm-clean li {
  padding-left: 0.2em;
  opacity: 0;
  transform: translateY(0.3em);
  transition:
    opacity 0.55s var(--hm-ease),
    transform 0.55s var(--hm-ease);
}
.is-clean .hm-clean li:nth-child(1) { transition-delay: 0.75s !important; }
.is-clean .hm-clean li:nth-child(2) { transition-delay: 0.9s !important; }
.is-clean .hm-clean li:nth-child(3) { transition-delay: 1.05s !important; }
.hm-clean li::marker {
  color: var(--hm-violet);
}

/* Sequenced, never overlapping: the spoken text leaves, then the message arrives. */
.is-clean .hm-spoken,
.is-hold .hm-spoken,
.is-fade .hm-spoken {
  opacity: 0;
  transform: translateY(-0.3em);
  transition-duration: 0.4s;
}
.is-clean .hm-clean {
  transition-delay: 0.45s;
}
/* On the restart the words reset to hidden; without this they would fade out
   visibly after the container comes back. Hide the container at once instead. */
.is-idle .hm-spoken {
  opacity: 0;
  transition: none;
}
.is-clean .hm-clean,
.is-hold .hm-clean,
.is-clean .hm-clean li,
.is-hold .hm-clean li {
  opacity: 1;
  transform: none;
}
.is-fade .hm-clean,
.is-fade .hm-field .text {
  opacity: 0;
}

/* Right: the phone, rising from below the panel edge */
.hm-phone {
  flex: none;
  align-self: flex-end;
  width: 30em;
  height: 48em;
  margin-bottom: -2em;
  padding: 1em 1em 0;
  border-radius: 4.6em 4.6em 0 0;
  background: var(--ld-navy-700);
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.08), 0 3em 8em rgba(0, 0, 0, 0.45);
}
.hm-screen {
  height: 100%;
  border-radius: 3.8em 3.8em 0 0;
  background: var(--ld-navy-975);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.hm-status {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1.3em 2.4em 0;
  font-size: 1.1em;
  font-weight: 600;
  color: rgba(255, 255, 255, 0.85);
}
.hm-status .island {
  width: 8em;
  height: 2.2em;
  border-radius: 1.1em;
  background: #000;
}
.hm-status .bars {
  display: flex;
  gap: 0.2em;
  align-items: flex-end;
}
.hm-status .bars i {
  width: 0.35em;
  background: rgba(255, 255, 255, 0.85);
  border-radius: 0.1em;
}
.hm-status .bars i:nth-child(1) { height: 0.5em; }
.hm-status .bars i:nth-child(2) { height: 0.75em; }
.hm-status .bars i:nth-child(3) { height: 1em; }

/* The chat gives way as the field grows, keeping the newest bubble in view */
.hm-chat {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding: 1.2em 1.8em 1.6em;
}
.hm-contact {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.4em;
  font-size: 1.1em;
  color: rgba(255, 255, 255, 0.7);
}
.hm-contact .avatar {
  width: 2.6em;
  height: 2.6em;
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-weight: 600;
  background: var(--ld-navy-700);
  color: rgba(255, 255, 255, 0.8);
}
.hm-bubble {
  align-self: flex-start;
  max-width: 80%;
  padding: 0.8em 1.1em;
  border-radius: 1.4em;
  background: var(--ld-navy-700);
  color: rgba(255, 255, 255, 0.9);
  font-size: 1.3em;
  line-height: 1.3;
}

.hm-field {
  flex: none;
  display: flex;
  align-items: flex-end;
  gap: 0.8em;
  margin: 0 1.4em 1.2em;
  padding: 0.75em 0.6em 0.75em 1.2em;
  border-radius: 1.6em;
  border: 1px solid rgba(255, 255, 255, 0.16);
  font-size: 1.2em;
  line-height: 1.35;
  color: #fff;
}
.hm-field > span {
  flex: 1;
  white-space: pre-wrap;
  transition: opacity 0.6s ease;
}
.hm-field .placeholder {
  color: rgba(255, 255, 255, 0.3);
}
.hm-field .caret {
  display: inline-block;
  width: 0.12em;
  height: 1.1em;
  margin: 0 0.1em;
  vertical-align: -0.15em;
  background: var(--hm-blue);
}
.hm-field .send {
  flex: none;
  position: relative;
  width: 1.9em;
  height: 1.9em;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.12);
  transition: background-color 0.3s ease;
}
.hm-field .send.on {
  background: var(--hm-blue);
}
/* Up arrow, as in Messages */
.hm-field .send .arrow {
  position: absolute;
  inset: 0.38em;
  background: #fff;
  -webkit-mask: url('/icon-arrow-up.svg') center / contain no-repeat;
  mask: url('/icon-arrow-up.svg') center / contain no-repeat;
}

/* Keyboard */
.hm-kb {
  flex: none;
  padding: 0.5em 0.7em 1.6em;
  background: var(--ld-navy-850);
  display: flex;
  flex-direction: column;
  gap: 0.75em;
}
/* Proportions measured from an iPhone 15 screenshot of the recording bar. */
.grabber {
  align-self: center;
  width: 2.9em;
  height: 0.35em;
  border-radius: 0.2em;
  background: rgba(255, 255, 255, 0.28);
  margin-bottom: 0.5em;
}
.hm-bar {
  position: relative;
  height: 2.7em;
  margin: 0 0.3em 0.2em;
  border-radius: 1.35em;
  background: var(--hm-bar);
}
.hm-bar .layer {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: opacity 0.25s ease;
}
/* The incoming layer waits for the outgoing one, so they never overlap. */
.hm-bar .layer.idle,
.hm-bar.rec .layer.rec {
  transition-delay: 0.25s;
}
.hm-bar.rec .layer.idle,
.hm-bar .layer.rec {
  transition-delay: 0s;
}
.hm-bar .layer.rec {
  opacity: 0;
}
.hm-bar.rec .layer.rec {
  opacity: 1;
}
.hm-bar.rec .layer.idle {
  opacity: 0;
}

/* Idle: mic.fill + "Diction" in the wordmark face, as the app draws it */
.hm-bar .idle {
  gap: 0.5em;
  color: #fff;
}
.hm-bar .mic {
  width: 1.3em;
  height: 1.75em;
  margin-top: -0.1em;
  background: #fff;
  -webkit-mask: url('/mic-fill.svg') center / contain no-repeat;
  mask: url('/mic-fill.svg') center / contain no-repeat;
}
.hm-bar .wordmark {
  font-family: 'BigShouldersInlineText', sans-serif;
  font-weight: 800;
  font-size: 1.75em;
  line-height: 1;
  letter-spacing: 0.02em;
  transform: translateY(0.04em);
}

/* Recording */
.hm-bar .meter {
  height: 2em;
  display: flex;
  align-items: center;
  gap: 0.3em;
}
.hm-bar .meter i {
  width: 0.22em;
  height: 100%;
  border-radius: 0.11em;
  background: #fff;
  will-change: transform;
}
.hm-bar .pause {
  position: absolute;
  left: 1.4em;
  display: flex;
  gap: 0.2em;
}
.hm-bar .pause i {
  width: 0.3em;
  height: 0.9em;
  border-radius: 0.08em;
  background: #fff;
}
.hm-bar .tick {
  position: absolute;
  right: 0.35em;
  width: 2em;
  height: 2em;
  border-radius: 50%;
  background: #fff;
  display: grid;
  place-items: center;
}
.hm-bar .tick i {
  width: 1.15em;
  height: 1.15em;
  background: var(--hm-bar);
  -webkit-mask: url('/icon-check.svg') center / contain no-repeat;
  mask: url('/icon-check.svg') center / contain no-repeat;
}

.hm-row {
  display: flex;
  gap: 0.45em;
}
/* The A row (9 keys) is inset, as on the phone. Grabber and bar come first. */
.hm-row:nth-child(4) {
  padding-inline: 5%;
}
.hm-row .key {
  height: 2.7em;
  border-radius: 0.6em;
  background: rgba(255, 255, 255, 0.17);
}
.hm-row .key.dim {
  background: rgba(255, 255, 255, 0.08);
}

/* Phones: the panel turns portrait, so stack the two halves */
@container (max-width: 640px) {
  .hm-stage {
    font-size: calc(100cqw / 36);
    flex-direction: column;
    justify-content: flex-end;
    gap: 2em;
    padding-top: 2.5em;
  }
  .hm-left {
    width: auto;
    align-self: stretch;
    padding: 0 1.4em;
  }
  .hm-spoken,
  .hm-clean {
    font-size: 1.7em;
  }
  .hm-phone {
    align-self: center;
    height: 30em;
  }
}
</style>
