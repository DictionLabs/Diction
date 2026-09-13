<script setup lang="ts">
// A one-line scripted strip under the hero media: Type, Speak, Fix, in sequence.
// Pure text and CSS, no fake device. Loops; pauses on hover; static under reduced motion.
import { onMounted, onUnmounted, ref } from 'vue'

type Phase = 'type' | 'speak' | 'fix'
const phase = ref<Phase>('type')

// TYPE: characters typed with an autocorrect snap
const typed = ref('')
const snapped = ref(false)
const TYPE_RAW = 'meet me at 3 tmrw'
const TYPE_FIXED = 'meet me at 3 tomorrow'

// SPEAK: waveform then words streaming
const recording = ref(false)
const spoken = ref<string[]>([])
const SPEAK_WORDS = 'um so can we push the demo to thursday'.split(' ')
const cleaned = ref(false)
const SPEAK_CLEAN = 'Can we push the demo to Thursday?'

// FIX: selection plus voice command, then replacement
const fixText = ref(SPEAK_CLEAN)
const selecting = ref(false)
const command = ref('')
const replaced = ref(false)
const FIX_CMD = 'make it Friday'
const FIX_RESULT = 'Can we push the demo to Friday?'

const bars = Array.from({ length: 18 }, (_, i) => ({ d: (i * 97) % 11, h: 0.35 + ((i * 53) % 7) / 10 }))

let token = 0
let paused = false
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
async function wait(ms: number, my: number) {
  await sleep(ms)
  while (paused && my === token) await sleep(120)
  if (my !== token) throw new Error('cancelled')
}

function reset() {
  typed.value = ''
  snapped.value = false
  recording.value = false
  spoken.value = []
  cleaned.value = false
  fixText.value = SPEAK_CLEAN
  selecting.value = false
  command.value = ''
  replaced.value = false
}

async function run(my: number) {
  try {
    while (my === token) {
      reset()
      phase.value = 'type'
      await wait(500, my)
      for (const ch of TYPE_RAW) {
        typed.value += ch
        await wait(55 + (ch === ' ' ? 60 : 0), my)
      }
      await wait(350, my)
      snapped.value = true
      typed.value = TYPE_FIXED
      await wait(1200, my)

      phase.value = 'speak'
      recording.value = true
      await wait(700, my)
      for (const w of SPEAK_WORDS) {
        spoken.value = [...spoken.value, w]
        await wait(190, my)
      }
      await wait(350, my)
      recording.value = false
      cleaned.value = true
      await wait(1400, my)

      phase.value = 'fix'
      await wait(500, my)
      selecting.value = true
      await wait(500, my)
      for (const ch of FIX_CMD) {
        command.value += ch
        await wait(45, my)
      }
      await wait(450, my)
      replaced.value = true
      fixText.value = FIX_RESULT
      selecting.value = false
      await wait(1700, my)
    }
  } catch {
    /* cancelled */
  }
}

function replay() {
  token += 1
  run(token)
}

onMounted(() => {
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    phase.value = 'fix'
    typed.value = TYPE_FIXED
    snapped.value = true
    spoken.value = SPEAK_WORDS
    cleaned.value = true
    fixText.value = FIX_RESULT
    replaced.value = true
    return
  }
  token += 1
  run(token)
})

onUnmounted(() => {
  token += 1
})
</script>

<template>
  <div class="hs" @mouseenter="paused = true" @mouseleave="paused = false">
    <div class="hs-steps">
      <button class="hs-step" :class="{ on: phase === 'type' }" type="button" @click="replay()">
        <span class="ld-mono">01</span><span class="hs-step-name">Type</span>
      </button>
      <button class="hs-step" :class="{ on: phase === 'speak' }" type="button" @click="replay()">
        <span class="ld-mono">02</span><span class="hs-step-name">Speak</span>
      </button>
      <button class="hs-step fix" :class="{ on: phase === 'fix' }" type="button" @click="replay()">
        <span class="ld-mono">03</span><span class="hs-step-name">Fix</span>
      </button>
    </div>

    <div class="hs-stage">
      <!-- TYPE -->
      <div v-show="phase === 'type'" class="hs-line">
        <span class="hs-field">
          <span class="hs-text" :class="{ snap: snapped }">{{ typed }}</span><span class="hs-caret"></span>
        </span>
        <span class="hs-note ld-mono" :class="{ show: snapped }">autocorrect: tmrw to tomorrow</span>
      </div>

      <!-- SPEAK -->
      <div v-show="phase === 'speak'" class="hs-line">
        <span class="hs-wave" :class="{ live: recording }" aria-hidden="true">
          <i v-for="(b, i) in bars" :key="i" :style="{ '--h': b.h, '--d': b.d * 60 + 'ms' }"></i>
        </span>
        <span class="hs-field grow">
          <template v-if="!cleaned">
            <span class="hs-word">{{ spoken.join(' ') }}</span>
            <span v-if="recording" class="hs-caret"></span>
          </template>
          <span v-else class="hs-text snap">{{ SPEAK_CLEAN }}</span>
        </span>
        <span class="hs-note ld-mono" :class="{ show: cleaned }">fillers removed, punctuation added</span>
      </div>

      <!-- FIX -->
      <div v-show="phase === 'fix'" class="hs-line">
        <span class="hs-field grow">
          <template v-if="!replaced">
            <span>Can we push the demo to </span><span class="hs-sel" :class="{ on: selecting }">Thursday</span><span>?</span>
          </template>
          <span v-else class="hs-text snap">{{ FIX_RESULT }}</span>
        </span>
        <span class="hs-cmd" :class="{ show: command.length > 0 && !replaced }">
          <span class="hs-mic" aria-hidden="true"></span>"{{ command }}"
        </span>
        <span class="hs-note ld-mono" :class="{ show: replaced }">only the selection changed</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.hs {
  display: grid;
  grid-template-columns: 300px minmax(0, 1fr);
  gap: 1.5rem;
  align-items: center;
  padding: 1.25rem 0 0;
  border-top: 1px solid rgba(255, 255, 255, 0.12);
  margin-top: clamp(1.5rem, 3vw, 2.5rem);
}

.hs-steps {
  display: flex;
  gap: 0.35rem;
}
.hs-step {
  appearance: none;
  background: transparent;
  border: 1px solid rgba(255, 255, 255, 0.12);
  color: rgba(255, 255, 255, 0.5);
  border-radius: 999px;
  padding: 0 12px;
  height: 36px;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  font: inherit;
  transition: color 0.25s, border-color 0.25s, background-color 0.25s;
}
.hs-step .ld-mono {
  color: inherit;
  font-size: 0.65rem;
}
.hs-step-name {
  font-size: 0.85rem;
  font-weight: 600;
}
.hs-step.on {
  color: #fff;
  border-color: var(--vp-c-brand-1);
  background: rgba(0, 122, 255, 0.14);
}
.hs-step.on.fix {
  border-color: var(--ld-violet);
  background: var(--ld-violet-soft);
}

.hs-stage {
  min-height: 44px;
  display: flex;
  align-items: center;
}
.hs-line {
  display: flex;
  align-items: center;
  gap: 1rem;
  width: 100%;
  min-width: 0;
}
.hs-field {
  display: inline-flex;
  align-items: center;
  min-height: 40px;
  padding: 0 14px;
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.1);
  color: #fff;
  font-size: 0.95rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}
.hs-field.grow {
  flex: 1 1 auto;
}
.hs-text.snap {
  color: #fff;
}
.hs-word {
  color: rgba(255, 255, 255, 0.85);
}
.hs-caret {
  display: inline-block;
  width: 1.5px;
  height: 1.1em;
  background: var(--vp-c-brand-1);
  margin-left: 2px;
  animation: hs-blink 1s steps(2) infinite;
}
@keyframes hs-blink {
  to { opacity: 0; }
}

.hs-note {
  color: rgba(255, 255, 255, 0.45) !important;
  text-transform: none !important;
  letter-spacing: 0.02em !important;
  opacity: 0;
  transform: translateY(4px);
  transition: opacity 0.3s, transform 0.3s;
  white-space: nowrap;
}
.hs-note.show {
  opacity: 1;
  transform: none;
}

.hs-wave {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  height: 22px;
  flex: 0 0 auto;
}
.hs-wave i {
  display: block;
  width: 2px;
  height: 100%;
  border-radius: 2px;
  background: var(--vp-c-brand-1);
  transform: scaleY(0.2);
  transform-origin: center;
  transition: transform 0.3s;
}
.hs-wave.live i {
  animation: hs-bar 0.9s ease-in-out infinite alternate;
  animation-delay: var(--d);
}
@keyframes hs-bar {
  from { transform: scaleY(0.15); }
  to { transform: scaleY(var(--h)); }
}

.hs-sel {
  border-radius: 3px;
  padding: 1px 2px;
  transition: background-color 0.3s;
}
.hs-sel.on {
  background: rgba(175, 82, 222, 0.5);
}
.hs-cmd {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 34px;
  padding: 0 12px;
  border-radius: 999px;
  border: 1px solid var(--ld-violet);
  color: #fff;
  font-size: 0.85rem;
  white-space: nowrap;
  opacity: 0;
  transition: opacity 0.25s;
}
.hs-cmd.show {
  opacity: 1;
}
.hs-mic {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--ld-violet);
}

@media (max-width: 900px) {
  .hs {
    grid-template-columns: minmax(0, 1fr);
    gap: 0.9rem;
  }
  .hs-note {
    display: none;
  }
}
@media (max-width: 640px) {
  .hs-step {
    padding: 0 10px;
  }
  .hs-line {
    flex-wrap: wrap;
    gap: 0.6rem;
  }
}
@media (prefers-reduced-motion: reduce) {
  .hs-caret,
  .hs-wave.live i {
    animation: none;
  }
}
</style>
