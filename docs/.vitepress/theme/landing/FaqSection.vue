<script setup lang="ts">
// Written for the two people who land here: a self-hoster and someone leaving
// another dictation app. Every answer must stay true for the App Store build.
import { faqs } from './faq'
</script>

<template>
  <section class="ld-section faq-landing">
    <div class="ld-container narrow">
      <div class="ld-head ld-center" v-reveal>
        <h2 class="ld-h2 big">Good questions.</h2>
      </div>

      <div class="faq-list" v-reveal="{ delay: 80 }">
        <details v-for="(faq, i) in faqs" :key="faq.q" class="faq-item" :open="i === 0">
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
  padding: 1.6rem 0;
  cursor: pointer;
  list-style: none;
  font-family: 'Libre Franklin', var(--vp-font-family-base);
  font-size: clamp(1.2rem, 1.9vw, 1.5rem);
  font-weight: 650;
  letter-spacing: -0.015em;
  line-height: 1.25;
  color: var(--vp-c-text-1);
  transition: color 0.2s ease;
}
.faq-summary:hover,
.faq-item[open] .faq-summary {
  color: var(--ld-violet-blue);
}
.faq-summary::-webkit-details-marker {
  display: none;
}
.faq-summary::marker {
  content: '';
}

/* Plus in a circle; turns into a minus when open. */
.faq-icon {
  position: relative;
  width: 36px;
  height: 36px;
  flex-shrink: 0;
  border-radius: 50%;
  border: 1px solid var(--vp-c-divider);
  transition: border-color 0.2s ease, background-color 0.2s ease;
}
.faq-item[open] .faq-icon {
  border-color: transparent;
  background: color-mix(in srgb, var(--ld-violet-blue) 12%, transparent);
}
.faq-icon .bar {
  position: absolute;
  inset: 0;
  margin: auto;
  width: 14px;
  height: 2px;
  border-radius: 2px;
  background: currentColor;
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
  margin: 0 0 1.75rem;
  padding-right: 3.5rem;
  max-width: 44rem;
  color: var(--vp-c-text-2);
  font-size: clamp(1.05rem, 1.4vw, 1.1875rem);
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
    padding: 1.25rem 0;
  }
  .faq-icon {
    width: 30px;
    height: 30px;
  }
}
</style>
