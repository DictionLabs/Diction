<script setup lang="ts">
const faqs = [
  {
    q: 'Is Diction free?',
    a: 'On-device and self-hosted are free with no word limits. Diction One cloud is a subscription with a free trial. See pricing in the app.',
  },
  {
    q: 'Does it work offline?',
    a: 'On-device mode does, once the speech model is downloaded. Cloud and self-hosted need a network.',
  },
  {
    q: 'Why does the keyboard need Full Access?',
    a: 'iOS requires it for any keyboard that uses the network. Diction uses it to send audio for transcription and nothing else.',
  },
  {
    q: 'Which languages?',
    a: '99 for dictation, auto-detected. 25 European languages use our most accurate engine.',
  },
  {
    q: 'Is my voice stored?',
    a: 'No. On-device never leaves the phone. Self-hosted goes only to your server. Diction One processes audio and discards it.',
  },
  {
    q: 'What is self-hosting?',
    a: 'You run the open-source server on your own hardware and point the app at it. One Docker command.',
  },
]
</script>

<template>
  <section class="ld-section faq-landing">
    <div class="ld-container">
      <div class="ld-label-row" v-reveal>
        <span class="ld-mono">09 / FAQ</span>
        <span class="ld-mono">Short answers</span>
      </div>
    </div>
    <div class="ld-container narrow">
      <div class="ld-head ld-center" v-reveal>
        <h2 class="ld-h2">Good questions.</h2>
      </div>

      <div class="faq-list" v-reveal="{ delay: 80 }">
        <details v-for="faq in faqs" :key="faq.q" class="faq-item">
          <summary class="faq-summary">
            <span class="faq-q">{{ faq.q }}</span>
            <span class="faq-icon" aria-hidden="true">
              <span class="bar bar-h"></span>
              <span class="bar bar-v"></span>
            </span>
          </summary>
          <div class="faq-body">
            <p class="faq-body-inner">{{ faq.a }}</p>
          </div>
        </details>
      </div>
    </div>
  </section>
</template>

<style scoped>
.faq-list {
  border-top: 1px solid var(--vp-c-divider);
}

.faq-item {
  border-bottom: 1px solid var(--vp-c-divider);
}

.faq-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1.5rem;
  padding: 1.375rem 0;
  cursor: pointer;
  list-style: none;
  font-size: 1.0625rem;
  font-weight: 600;
  color: var(--vp-c-text-1);
}
.faq-summary::-webkit-details-marker {
  display: none;
}
.faq-summary::marker {
  content: '';
}

.faq-icon {
  position: relative;
  width: 18px;
  height: 18px;
  flex-shrink: 0;
}
.faq-icon .bar {
  position: absolute;
  inset: 0;
  margin: auto;
  width: 100%;
  height: 2px;
  border-radius: 2px;
  background: var(--vp-c-text-2);
  transition: transform 0.25s var(--ld-ease);
}
.faq-icon .bar-v {
  transform: rotate(90deg);
}
.faq-item[open] .faq-icon .bar-v {
  transform: rotate(0deg);
}

.faq-body {
  display: grid;
  grid-template-rows: 0fr;
  transition: grid-template-rows 0.3s var(--ld-ease);
}
.faq-item[open] .faq-body {
  grid-template-rows: 1fr;
}
.faq-body-inner {
  overflow: hidden;
  min-height: 0;
}
.faq-body-inner p,
.faq-body p {
  margin: 0 0 1.375rem;
  padding-right: 2.5rem;
  color: var(--vp-c-text-2);
  font-size: 0.9375rem;
  line-height: 1.6;
}

/* Force the content wrapper to stay laid out (grid) instead of the
   browser's default display:none when the <details> is closed, so the
   grid-template-rows transition can actually animate. */
.faq-item:not([open]) .faq-body,
.faq-item[open] .faq-body {
  display: grid;
}

@media (max-width: 640px) {
  .faq-summary {
    font-size: 1rem;
    padding: 1.125rem 0;
  }
}
</style>
