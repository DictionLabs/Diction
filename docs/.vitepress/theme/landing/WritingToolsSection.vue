<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'

/**
 * Deterministic cleanup demo. Four independent transforms run over a token
 * fixture (fillers, grammar, formatting, tone), so every toggle combination
 * renders a real result. No model, no fake latency.
 */
interface Tok {
  /** Raw spoken token, punctuation attached. */
  t: string
  /** Text once fillers are removed. '' means the token itself is a filler. */
  f?: string
  /** Grammar-corrected text. */
  g?: string
  /** Formal-register text. */
  formal?: string
  /** Text once formatting applies. '' drops the token. */
  fmt?: string
  /** List item index when formatting is on. */
  item?: number
}

const sentences: Tok[][] = [
  [
    { t: 'um', f: '' },
    { t: 'so', f: '' },
    { t: 'quick', formal: 'a brief' },
    { t: 'update' },
    { t: 'for' },
    { t: 'tomorrow.' },
  ],
  [
    { t: "we're", formal: 'we' },
    { t: 'gonna', g: 'going to', formal: 'will' },
    { t: 'need' },
    { t: 'three' },
    { t: 'things,', fmt: 'things:' },
    { t: 'uh,', f: '', item: 1 },
    { t: 'the', item: 1 },
    { t: 'slides,', item: 1 },
    { t: 'the', item: 2 },
    { t: 'demo', item: 2 },
    { t: 'laptop', item: 2 },
    { t: 'and,', f: 'and', fmt: '' },
    { t: 'like,', f: '', item: 3 },
    { t: 'someone', item: 3 },
    { t: 'to', item: 3 },
    { t: 'take', item: 3 },
    { t: 'notes.', item: 3 },
  ],
  [
    { t: 'also', g: 'Also,', formal: 'in addition,' },
    { t: 'their', g: "they're", formal: 'they' },
    { t: 'gonna', g: 'going to', formal: 'will' },
    { t: 'send' },
    { t: 'the' },
    { t: 'contract' },
    { t: 'tonight', g: 'tonight,' },
    { t: 'so' },
    { t: "i'll", g: "I'll", formal: 'I will' },
    { t: 'forward' },
    { t: 'it', g: 'it.' },
  ],
]

const fillers = ref(false)
const grammar = ref(false)
const formatting = ref(false)
const tone = ref<'casual' | 'formal'>('casual')

const wordCount = sentences.reduce((n, s) => n + s.length, 0)

interface Run {
  text: string
  changed: boolean
}
interface Block {
  kind: 'p' | 'li'
  n?: number
  runs: Run[]
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

const blocks = computed<Block[]>(() => {
  const out: Block[] = []
  for (const sentence of sentences) {
    let first = true
    for (const tok of sentence) {
      const base = fillers.value && tok.f !== undefined ? tok.f : tok.t
      let text = base
      if (grammar.value && tok.g !== undefined) text = tok.g
      if (tone.value === 'formal' && tok.formal !== undefined) text = tok.formal
      let changed = text !== base
      if (formatting.value && tok.fmt !== undefined) text = tok.fmt
      if (text === '') continue
      if (first && grammar.value) {
        const cap = capitalize(text)
        if (cap !== text) {
          text = cap
          changed = true
        }
      }
      first = false
      const run: Run = { text, changed }
      const last = out[out.length - 1]
      if (formatting.value && tok.item) {
        if (last && last.kind === 'li' && last.n === tok.item) last.runs.push(run)
        else out.push({ kind: 'li', n: tok.item, runs: [run] })
      } else if (last && last.kind === 'p') {
        last.runs.push(run)
      } else {
        out.push({ kind: 'p', runs: [run] })
      }
    }
  }
  for (const b of out) {
    if (b.kind !== 'li' || !b.runs.length) continue
    const tail = b.runs[b.runs.length - 1]
    tail.text = tail.text.replace(/[.,]$/, '')
    b.runs[0].text = capitalize(b.runs[0].text)
  }
  return out
})

const fillersRemoved = computed(() =>
  fillers.value ? sentences.flat().filter((t) => t.f === '').length : 0,
)
const fixes = computed(() => blocks.value.reduce((n, b) => n + b.runs.filter((r) => r.changed).length, 0))
const listItems = computed(() => blocks.value.filter((b) => b.kind === 'li').length)
const stateKey = computed(
  () => `${fillers.value ? 1 : 0}${grammar.value ? 1 : 0}${formatting.value ? 1 : 0}${tone.value}`,
)

/* ---------- Auto-play on reveal ---------- */
const root = ref<HTMLElement | null>(null)
let timers: ReturnType<typeof setTimeout>[] = []
let observer: IntersectionObserver | null = null
let played = false

function clearTimers() {
  for (const id of timers) clearTimeout(id)
  timers = []
}

function play() {
  if (played) return
  played = true
  timers.push(setTimeout(() => (fillers.value = true), 500))
  timers.push(setTimeout(() => (grammar.value = true), 1400))
  timers.push(setTimeout(() => (formatting.value = true), 2300))
}

function userToggle(which: 'fillers' | 'grammar' | 'formatting') {
  clearTimers()
  played = true
  if (which === 'fillers') fillers.value = !fillers.value
  else if (which === 'grammar') grammar.value = !grammar.value
  else formatting.value = !formatting.value
}

function setTone(next: 'casual' | 'formal') {
  clearTimers()
  played = true
  tone.value = next
}

onMounted(() => {
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    fillers.value = true
    grammar.value = true
    formatting.value = true
    played = true
    return
  }
  if (!root.value) return
  observer = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        observer?.disconnect()
        observer = null
        play()
      }
    },
    { threshold: 0.35 },
  )
  observer.observe(root.value)
})

onUnmounted(() => {
  clearTimers()
  observer?.disconnect()
  observer = null
})

const tiles = [
  {
    label: 'Fillers',
    line: 'um, uh, like, you know, false starts',
    before: 'so um basically yes',
    after: 'Yes.',
  },
  {
    label: 'Grammar',
    line: 'Agreement, tense, apostrophes, capitalization',
    before: 'their gonna',
    after: "they're going to",
  },
  {
    label: 'Formatting',
    line: 'Lists, numbers, dates, paragraphs',
    before: 'one two three',
    after: '1. 2. 3.',
  },
]
</script>

<template>
  <section class="ld-section soft wt-section" ref="root">
    <div class="ld-container">
      <div class="ld-label-row" v-reveal>
        <span class="ld-mono ld-violet">03 / Writing Tools</span>
        <span class="ld-mono">diction.one</span>
      </div>

      <div class="ld-head wt-head" v-reveal>
        <h2 class="ld-h2">Exactly what you said. Cleaned up.</h2>
        <p class="ld-lead">
          Fast, accurate transcription, then the cleanup you would do by hand: fillers gone, grammar fixed,
          lists formatted.
        </p>
      </div>

      <div class="ld-card wt-panel" v-reveal>
        <!-- Left pane header -->
        <div class="wt-pane-head wt-left-head">
          <span class="ld-mono wt-transcript-label">Transcript</span>
          <span class="ld-mono wt-meta">as spoken · {{ wordCount }} words</span>
        </div>

        <!-- Right pane header: controls -->
        <div class="wt-pane-head wt-controls">
          <div class="wt-switches" role="group" aria-label="Cleanup options">
            <button
              type="button"
              class="wt-switch"
              role="switch"
              :aria-checked="fillers"
              @click="userToggle('fillers')"
            >
              <span class="wt-track" aria-hidden="true"><span class="wt-knob"></span></span>
              <span class="wt-switch-label">Filler words</span>
            </button>
            <button
              type="button"
              class="wt-switch"
              role="switch"
              :aria-checked="grammar"
              @click="userToggle('grammar')"
            >
              <span class="wt-track" aria-hidden="true"><span class="wt-knob"></span></span>
              <span class="wt-switch-label">Grammar</span>
            </button>
            <button
              type="button"
              class="wt-switch"
              role="switch"
              :aria-checked="formatting"
              @click="userToggle('formatting')"
            >
              <span class="wt-track" aria-hidden="true"><span class="wt-knob"></span></span>
              <span class="wt-switch-label">Formatting</span>
            </button>
          </div>
          <div class="wt-tone" role="radiogroup" aria-label="Tone">
            <span class="ld-mono wt-tone-label">Tone</span>
            <div class="wt-seg">
              <button
                type="button"
                role="radio"
                :aria-checked="tone === 'casual'"
                :class="{ on: tone === 'casual' }"
                @click="setTone('casual')"
              >
                Casual
              </button>
              <button
                type="button"
                role="radio"
                :aria-checked="tone === 'formal'"
                :class="{ on: tone === 'formal' }"
                @click="setTone('formal')"
              >
                Formal
              </button>
            </div>
          </div>
        </div>

        <!-- Left pane body: raw transcript -->
        <div class="wt-pane wt-left">
          <p class="wt-raw">
            <template v-for="(sentence, si) in sentences" :key="si">
              <template v-for="(tok, ti) in sentence" :key="ti">
                <span
                  class="wt-tok"
                  :class="{
                    struck: fillers && tok.f === '',
                    fix: (grammar && tok.g !== undefined) || (tone === 'formal' && tok.formal !== undefined),
                  }"
                  >{{ tok.t }}</span
                >{{ ' ' }}
              </template>
            </template>
          </p>
        </div>

        <!-- Right pane body: result -->
        <div class="wt-pane wt-right">
          <span class="ld-mono ld-violet wt-result-label">Result</span>
          <div class="wt-result" :key="stateKey">
            <template v-for="(b, bi) in blocks" :key="bi">
              <p v-if="b.kind === 'p'" class="wt-p">
                <template v-for="(r, ri) in b.runs" :key="ri"
                  >{{ ri ? ' ' : '' }}<mark v-if="r.changed" class="wt-hl">{{ r.text }}</mark
                  ><template v-else>{{ r.text }}</template></template
                >
              </p>
              <p v-else class="wt-li">
                <span class="wt-li-n">{{ b.n }}.</span>
                <span
                  ><template v-for="(r, ri) in b.runs" :key="ri"
                    >{{ ri ? ' ' : '' }}<mark v-if="r.changed" class="wt-hl">{{ r.text }}</mark
                    ><template v-else>{{ r.text }}</template></template
                  ></span
                >
              </p>
            </template>
          </div>
          <div class="wt-status ld-mono" aria-live="polite">
            <template v-if="!fillersRemoved && !fixes && !listItems">unchanged</template>
            <template v-else>
              <span v-if="fillersRemoved">{{ fillersRemoved }} fillers removed</span>
              <span v-if="fixes">{{ fixes }} fixes</span>
              <span v-if="listItems">list of {{ listItems }}</span>
            </template>
          </div>
        </div>
      </div>

      <div class="wt-tiles ld-stagger" v-reveal>
        <div v-for="t in tiles" :key="t.label" class="wt-tile">
          <span class="ld-mono wt-tile-label">{{ t.label }}</span>
          <p class="wt-tile-line">{{ t.line }}</p>
          <p class="wt-tile-ba">
            <span class="wt-ba-before">{{ t.before }}</span>
            <span class="wt-ba-arrow" aria-hidden="true">→</span>
            <span class="wt-ba-after">{{ t.after }}</span>
          </p>
        </div>
      </div>

      <div class="ld-rule wt-foot-rule" v-reveal></div>
      <p class="ld-small wt-foot" v-reveal>
        <span>Part of Diction One. Free on your own server with your own language model.</span>
        <a href="/features/writing-tools">About Writing Tools</a>
      </p>
    </div>
  </section>
</template>

<style scoped>
.wt-head {
  max-width: 760px;
}

.wt-head .ld-lead {
  max-width: 640px;
}

/* ---------- Panel ---------- */
.wt-panel {
  display: grid;
  grid-template-columns: 0.95fr 1.05fr;
  grid-template-rows: auto 1fr;
  overflow: hidden;
}

.wt-pane-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 0.75rem 1.25rem;
  min-height: 60px;
  padding: 0.5rem 1.5rem;
  border-bottom: 1px solid var(--vp-c-divider);
}

.wt-left-head {
  background: var(--ld-surface-2);
  border-right: 1px solid var(--vp-c-divider);
}

.wt-transcript-label {
  color: var(--vp-c-brand-1);
}

.wt-meta {
  text-transform: none;
  letter-spacing: 0.02em;
}

.wt-pane {
  padding: 1.5rem;
  min-width: 0;
}

.wt-left {
  background: var(--ld-surface-2);
  border-right: 1px solid var(--vp-c-divider);
}

.wt-right {
  display: flex;
  flex-direction: column;
}

.wt-result-label {
  display: block;
  margin-bottom: 0.9rem;
}

/* ---------- Raw transcript ---------- */
.wt-raw {
  font-family: var(--vp-font-family-mono);
  font-size: 1rem;
  line-height: 1.85;
  color: var(--vp-c-text-2);
  text-wrap: pretty;
}

.wt-tok {
  transition: opacity 0.5s var(--ld-ease), color 0.5s, text-decoration-color 0.5s;
  text-decoration: line-through transparent;
  text-decoration-thickness: 1.5px;
}

.wt-tok.struck {
  opacity: 0.32;
  text-decoration-color: currentColor;
}

.wt-tok.fix {
  text-decoration: underline dotted var(--ld-violet);
  text-underline-offset: 3px;
  color: var(--vp-c-text-1);
}

/* ---------- Result ---------- */
.wt-result {
  flex: 1;
  font-size: 1.0625rem;
  line-height: 1.6;
  color: var(--vp-c-text-1);
  animation: wt-swap 0.35s var(--ld-ease);
}

@keyframes wt-swap {
  from {
    opacity: 0.4;
    transform: translateY(4px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

.wt-p + .wt-p,
.wt-li + .wt-p {
  margin-top: 0.85rem;
}

.wt-p + .wt-li {
  margin-top: 0.6rem;
}

.wt-li {
  display: flex;
  gap: 0.6rem;
  padding-left: 0.25rem;
}

.wt-li + .wt-li {
  margin-top: 0.2rem;
}

.wt-li-n {
  flex: 0 0 auto;
  min-width: 1.4em;
  font-family: var(--vp-font-family-mono);
  font-size: 0.875rem;
  color: var(--ld-violet);
  line-height: inherit;
}

.wt-hl {
  background: var(--ld-violet-soft);
  color: inherit;
  border-radius: 4px;
  padding: 0 0.2em;
  margin: 0 -0.05em;
  box-decoration-break: clone;
  -webkit-box-decoration-break: clone;
}

.wt-status {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem 1.25rem;
  margin-top: 1.25rem;
  padding-top: 0.9rem;
  border-top: 1px solid var(--vp-c-divider);
}

/* ---------- Switches ---------- */
.wt-switches {
  display: flex;
  flex-wrap: wrap;
  gap: 0 0.25rem;
}

.wt-switch {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 44px;
  padding: 0 8px 0 4px;
  border: 0;
  background: transparent;
  color: var(--vp-c-text-1);
  font: inherit;
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  border-radius: 8px;
}

.wt-switch:focus-visible,
.wt-seg button:focus-visible {
  outline: 2px solid var(--ld-violet);
  outline-offset: 2px;
}

.wt-track {
  position: relative;
  display: inline-block;
  width: 40px;
  height: 24px;
  border-radius: 12px;
  background: color-mix(in srgb, var(--vp-c-text-3) 45%, transparent);
  transition: background-color 0.25s var(--ld-ease);
}

.wt-knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.3);
  transition: transform 0.25s var(--ld-ease);
}

.wt-switch[aria-checked='true'] .wt-track {
  background: var(--ld-violet);
}

.wt-switch[aria-checked='true'] .wt-knob {
  transform: translateX(16px);
}

/* ---------- Tone segmented control ---------- */
.wt-tone {
  display: inline-flex;
  align-items: center;
  gap: 10px;
}

.wt-seg {
  display: inline-flex;
  padding: 3px;
  border-radius: var(--ld-r-pill);
  background: color-mix(in srgb, var(--vp-c-text-3) 18%, transparent);
}

.wt-seg button {
  min-height: 34px;
  padding: 0 14px;
  border: 0;
  border-radius: var(--ld-r-pill);
  background: transparent;
  color: var(--vp-c-text-2);
  font: inherit;
  font-size: 0.8125rem;
  font-weight: 600;
  cursor: pointer;
  transition: background-color 0.2s, color 0.2s, box-shadow 0.2s;
}

.wt-seg button.on {
  background: var(--ld-surface);
  color: var(--vp-c-text-1);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.15);
}

/* ---------- Tiles ---------- */
.wt-tiles {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1rem;
  margin-top: 1.25rem;
}

.wt-tile {
  padding: 1.1rem 1.25rem;
  border: 1px solid var(--vp-c-divider);
  border-radius: 14px;
  background: var(--ld-surface);
}

.wt-tile-label {
  display: block;
  color: var(--ld-violet);
}

.wt-tile-line {
  margin-top: 0.4rem;
  font-size: 0.9375rem;
  line-height: 1.45;
  color: var(--vp-c-text-1);
}

.wt-tile-ba {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.35rem 0.5rem;
  margin-top: 0.75rem;
  padding-top: 0.75rem;
  border-top: 1px solid var(--vp-c-divider);
  font-family: var(--vp-font-family-mono);
  font-size: 0.8125rem;
  line-height: 1.4;
}

.wt-ba-before {
  color: var(--vp-c-text-3);
}

.wt-ba-arrow {
  color: var(--ld-violet);
}

.wt-ba-after {
  color: var(--vp-c-text-1);
  font-weight: 600;
}

/* ---------- Footer ---------- */
.wt-foot-rule {
  margin-top: clamp(2.5rem, 5vw, 4rem);
}

.wt-foot {
  margin-top: 1rem;
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem 1rem;
  justify-content: space-between;
}

.wt-foot a {
  color: var(--ld-violet);
  font-weight: 500;
  white-space: nowrap;
}

.wt-foot a:hover {
  text-decoration: underline;
}

/* ---------- Mobile ---------- */
@media (max-width: 860px) {
  .wt-panel {
    grid-template-columns: 1fr;
    grid-template-rows: none;
  }
  .wt-left-head {
    order: 1;
    border-right: 0;
  }
  .wt-left {
    order: 2;
    border-right: 0;
    border-bottom: 1px solid var(--vp-c-divider);
  }
  .wt-controls {
    order: 3;
    flex-direction: column;
    align-items: stretch;
    gap: 0.25rem;
    padding: 0.75rem 1.25rem;
  }
  .wt-switches {
    flex-wrap: wrap;
  }
  .wt-tone {
    justify-content: space-between;
    min-height: 44px;
  }
  .wt-right {
    order: 4;
  }
  .wt-pane {
    padding: 1.25rem;
  }
  .wt-tiles {
    grid-template-columns: 1fr;
  }
  .wt-foot {
    flex-direction: column;
  }
}

@media (prefers-reduced-motion: reduce) {
  .wt-result,
  .wt-tok,
  .wt-track,
  .wt-knob {
    animation: none !important;
    transition: none !important;
  }
}
</style>
