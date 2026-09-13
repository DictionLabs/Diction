<script setup lang="ts">
import { computed } from 'vue'

interface Row {
  greetings: string[]
  duration: number // seconds
  reverse: boolean
}

const rows: Row[] = [
  {
    greetings: ['Hallo', 'Bonjour', 'Ahoj', 'Hola', 'Ciao', 'Olá', 'Hej', 'Cześć'],
    duration: 55,
    reverse: false,
  },
  {
    greetings: ['Привіт', 'Γειά', 'Merhaba', 'こんにちは', '안녕하세요', '你好', 'नमस्ते'],
    duration: 65,
    reverse: true,
  },
  {
    greetings: ['مرحبا', 'Hello', 'Hallo', 'Salut', 'Terve', 'Szia', 'Sveiki'],
    duration: 70,
    reverse: false,
  },
]

const tracks = computed(() => rows.map((row) => [...row.greetings, ...row.greetings]))

</script>

<template>
  <section class="ld-section soft lang-section">
    <div class="ld-container" v-reveal>
      <div class="ld-label-row">
        <span class="ld-mono ld-accent ld-blue">06 / 99 languages</span>
        <span class="ld-mono">Auto-detect on by default</span>
      </div>
    </div>
    <div class="ld-container ld-center ld-head" v-reveal>
      <h2 class="ld-h2">Speak in your language. Or switch halfway.</h2>
      <p class="ld-lead">Auto-detect is on by default. Switch mid-sentence and it keeps up.</p>
    </div>

    <div class="lang-rows" v-reveal="{ delay: 160 }">
      <div v-for="(row, ri) in rows" :key="ri" class="lang-row">
        <div
          class="lang-track"
          :style="{
            animationDuration: row.duration + 's',
            animationDirection: row.reverse ? 'reverse' : 'normal',
          }"
        >
          <span v-for="(g, gi) in tracks[ri]" :key="gi" class="lang-chip" dir="auto">{{ g }}</span>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.lang-section {
  overflow: hidden;
}

.lang-stats {
  margin-top: 1.75rem;
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 10px;
}

.lang-rows {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  margin-top: clamp(2.5rem, 5vw, 3.5rem);
}

.lang-row {
  overflow: hidden;
  -webkit-mask-image: linear-gradient(to right, transparent, #000 10%, #000 90%, transparent);
  mask-image: linear-gradient(to right, transparent, #000 10%, #000 90%, transparent);
}

.lang-track {
  display: flex;
  width: max-content;
  gap: 1rem;
  animation-name: lang-marquee;
  animation-timing-function: linear;
  animation-iteration-count: infinite;
}

.lang-row:hover .lang-track {
  animation-play-state: paused;
}

.lang-chip {
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
  padding: 0.6em 1.4em;
  border-radius: var(--ld-r-card);
  border: 1px solid var(--vp-c-divider);
  background: var(--ld-surface);
  box-shadow: var(--ld-shadow-sm);
  font-size: clamp(1.5rem, 3.4vw, 2.5rem);
  font-weight: 600;
  letter-spacing: -0.01em;
  color: var(--vp-c-text-1);
  white-space: nowrap;
}

@keyframes lang-marquee {
  from {
    transform: translateX(0);
  }
  to {
    transform: translateX(-50%);
  }
}

@media (max-width: 640px) {
  .lang-chip {
    padding: 0.5em 1.1em;
  }
}

@media (prefers-reduced-motion: reduce) {
  .lang-track {
    animation: none !important;
    transform: translateX(0) !important;
  }
  .lang-row {
    overflow-x: auto;
    -webkit-overflow-scrolling: touch;
    -webkit-mask-image: none;
    mask-image: none;
  }
  .lang-chip:nth-child(n + 6) {
    display: none;
  }
}
</style>
