<script setup lang="ts">
import { useData } from 'vitepress'

const { isDark } = useData()

interface Row {
  num: string
  label: string
  h2: string
  lead: string
  bullets: string[]
  /** Placeholder photo shown until a real app recording exists. */
  image: string
  /**
   * MEDIA SLOT: set `video` (or `videoLight` + `videoDark`) to a `/something.mp4`
   * path and the frame renders <video autoplay muted loop playsinline> instead
   * of the placeholder image. Leave undefined to keep the placeholder.
   */
  video?: string
  videoLight?: string
  videoDark?: string
  /** Phone recordings are portrait: centered in the frame over a blurred copy. */
  portrait?: boolean
  /** Things you can say, shown as chips under the lead. Same words as the App Store slides. */
  actions?: string[]
  /** Accent: blue is voice (default), violet is Writing Tools, orange is typing. */
  tone?: 'violet' | 'orange'
}

const rows: Row[] = [
  {
    num: '01',
    label: 'Speak',
    h2: 'Tap the mic. Text appears.',
    lead: 'Dictate in any app, in your language, for as long as you like. Fillers and false starts come out clean.',
    bullets: ['Words show up as you talk', 'No word limits, no daily caps', 'Works offline on your iPhone'],
    image: '/placeholder/speak.jpg',
  },
  {
    num: '02',
    label: 'Edit',
    tone: 'violet',
    h2: "Talk to what's on screen.",
    lead: 'Select a sentence and say what should change. The rest of the message stays as it was.',
    bullets: [
      'Edits the selection, not the whole text',
      'Hold the mic to edit by voice',
      'Sounds like you, not like AI',
    ],
    image: '/placeholder/fix.jpg',
    actions: ['Translate', 'Format', 'Fix grammar', 'Rewrite', 'Summarize'],
  },
  {
    num: '03',
    label: 'Type',
    tone: 'orange',
    h2: 'A keyboard that keeps up.',
    lead: 'A full keyboard on our own autocorrect engine, built to match the iPhone keyboard on accuracy and speed. It learns your names and jargon instead of correcting them away.',
    bullets: [
      'Layouts for English, German, Czech, French and Spanish',
      'Long-press accents in 47 languages',
      'A key row you arrange yourself',
    ],
    image: '/placeholder/type.jpg',
  },
]

function videoFor(row: Row): string | undefined {
  if (row.videoLight || row.videoDark) return isDark.value ? row.videoDark ?? row.videoLight : row.videoLight ?? row.videoDark
  return row.video
}
</script>

<template>
  <section class="ld-section story-section">
    <div class="ld-container">
      <div class="ld-label-row" v-reveal>
        <span class="ld-mono ld-accent ld-blue"><b class="ld-idx">02</b>What it does</span>
        <span class="ld-mono">One keyboard, everywhere</span>
      </div>

      <div class="ld-center ld-head story-head" v-reveal>
        <h2 class="ld-h2 story-title">Speak. Edit. Type.</h2>
        <p class="ld-lead">One keyboard that does the three things you do with text all day.</p>
      </div>

      <div class="story-rows">
        <article
          v-for="(row, i) in rows"
          :key="row.num"
          class="story-row"
          :class="[row.tone, { flip: i % 2 === 1 }]"
        >
          <div class="story-media-col" v-reveal>
            <div class="story-media" :class="{ 'kb-alt': i % 2 === 1 }">
              <!-- MEDIA SLOT: <video autoplay muted loop playsinline> replaces the placeholder once a real recording exists -->
              <video
                v-if="videoFor(row) && row.portrait"
                class="story-asset story-blur"
                :src="videoFor(row)"
                autoplay
                muted
                loop
                playsinline
                aria-hidden="true"
              />
              <video
                v-if="videoFor(row)"
                class="story-asset"
                :class="{ portrait: row.portrait }"
                :src="videoFor(row)"
                autoplay
                muted
                loop
                playsinline
                aria-hidden="true"
              />
              <img v-else class="story-asset" :src="row.image" alt="" loading="lazy" decoding="async" />
              <span v-if="!videoFor(row)" class="story-tag ld-mono">Placeholder</span>
              <span class="story-corner ld-mono">{{ row.num }}</span>
            </div>
          </div>

          <div class="story-text ld-stagger" v-reveal="{ delay: 100 }">
            <p class="ld-item story-num" :class="row.tone">
              <span class="ld-item-num">{{ row.num }}</span>
              <span>{{ row.label }}</span>
            </p>
            <h3 class="ld-h2 story-h2">{{ row.h2 }}</h3>
            <p class="ld-lead story-lead">{{ row.lead }}</p>
            <ul v-if="row.actions" class="story-actions" aria-label="Things you can ask for">
              <li v-for="a in row.actions" :key="a" class="story-action">{{ a }}</li>
            </ul>
            <ul class="story-list">
              <li v-for="b in row.bullets" :key="b">
                <span class="ld-check" aria-hidden="true"></span>
                <span>{{ b }}</span>
              </li>
            </ul>
          </div>
        </article>
      </div>

    </div>
  </section>
</template>

<style scoped>
.story-section {
  position: relative;
}

.story-head {
  margin-bottom: clamp(3rem, 7vw, 5.5rem);
}

.story-title {
  font-size: clamp(2.5rem, 7vw, 5rem);
  letter-spacing: -0.04em;
}

/* ---------- Rows ---------- */
.story-rows {
  display: flex;
  flex-direction: column;
  gap: clamp(3.5rem, 8vw, 7rem);
}

.story-row {
  display: grid;
  grid-template-columns: 1.1fr 0.9fr;
  grid-template-areas: 'media text';
  align-items: center;
  gap: clamp(2rem, 5vw, 4.5rem);
}

.story-row.flip {
  grid-template-columns: 0.9fr 1.1fr;
  grid-template-areas: 'text media';
}

.story-media-col {
  grid-area: media;
  min-width: 0;
}

.story-text {
  grid-area: text;
  min-width: 0;
}

/* Media bleeds past the container edge on wide screens (section clips overflow). */
@media (min-width: 1024px) {
  .story-row:not(.flip) .story-media-col {
    margin-left: clamp(0px, -3vw, -40px);
  }
  .story-row.flip .story-media-col {
    margin-right: clamp(0px, -3vw, -40px);
  }
}

/* Portrait recordings: contained over a blurred backdrop */
.story-asset.portrait {
  object-fit: contain;
  position: relative;
  z-index: 1;
}
.story-blur {
  position: absolute;
  inset: 0;
  filter: blur(28px) brightness(0.6);
  transform: scale(1.15);
}

/* ---------- Media frame ---------- */
.story-media {
  position: relative;
  aspect-ratio: 4 / 3;
  border-radius: 16px;
  overflow: hidden;
  background: var(--ld-surface-2);
  border: 1px solid var(--vp-c-divider);
  box-shadow: var(--ld-shadow-md);
  isolation: isolate;
}

.story-asset {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
  transform-origin: center;
  animation: story-kenburns 12s ease-in-out infinite alternate;
}

.story-media.kb-alt .story-asset {
  animation-direction: alternate-reverse;
}

@keyframes story-kenburns {
  from {
    transform: scale(1);
  }
  to {
    transform: scale(1.04);
  }
}

.story-tag,
.story-corner {
  position: absolute;
  z-index: 1;
  display: inline-flex;
  align-items: center;
  height: 24px;
  padding: 0 8px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--ld-navy-900) 72%, transparent);
  color: rgba(255, 255, 255, 0.9);
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
  font-size: 0.6875rem;
}

.story-tag {
  left: 12px;
  bottom: 12px;
}

.story-corner {
  top: 12px;
  right: 12px;
}

/* ---------- Text column ---------- */
.story-num {
  margin-bottom: 1.1rem;
}

/* The Edit row is the Writing Tools side of the keyboard: violet, not blue */
.story-row.violet .story-corner {
  color: var(--ld-violet);
}
.story-row.violet .ld-check {
  background-color: var(--ld-violet);
}


.story-h2 {
  font-size: clamp(1.9rem, 3.6vw, 2.75rem);
  margin-bottom: 1rem;
}

.story-lead {
  font-size: clamp(1rem, 1.4vw, 1.125rem);
  max-width: 480px;
}

.story-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 1.25rem;
}
.story-actions .story-action {
  margin: 0;
  padding: 6px 12px;
  border-radius: 999px;
  font-size: 0.875rem;
  font-weight: 500;
  line-height: 1.2;
  color: var(--vp-c-brand-1);
  background: color-mix(in srgb, var(--vp-c-brand-1) 10%, transparent);
}
.story-row.violet .story-action {
  color: var(--ld-violet);
  background: var(--ld-violet-soft);
}
.story-row.orange .story-corner {
  color: var(--ld-orange);
}
.story-row.orange .ld-check {
  background-color: var(--ld-orange);
}
.story-row.orange .story-action {
  color: var(--ld-orange);
  background: var(--ld-orange-soft);
}

.story-list {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  margin-top: 1.5rem;
  padding-top: 1.25rem;
  border-top: 1px solid var(--vp-c-divider);
}

.story-list li {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 0.9375rem;
  font-weight: 500;
  line-height: 1.4;
  color: var(--vp-c-text-1);
}

/* ---------- Footer line ---------- */
.story-foot-rule {
  margin-top: clamp(3rem, 7vw, 5.5rem);
}

.story-foot {
  margin-top: 1rem;
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem 1rem;
  justify-content: space-between;
}

.story-foot a {
  color: var(--vp-c-brand-1);
  font-weight: 500;
  white-space: nowrap;
}

.story-foot a:hover {
  text-decoration: underline;
}

/* ---------- Mobile: text above media, full-width frames ---------- */
@media (max-width: 860px) {
  .story-row,
  .story-row.flip {
    grid-template-columns: 1fr;
    grid-template-areas:
      'text'
      'media';
    gap: 1.5rem;
  }
  .story-media {
    aspect-ratio: 4 / 5;
  }
  .story-num {
    margin-bottom: 0.9rem;
  }
  .story-lead {
    max-width: none;
  }
  .story-foot {
    flex-direction: column;
  }
}

@media (prefers-reduced-motion: reduce) {
  .story-asset {
    animation: none !important;
    transform: none !important;
  }
}
</style>
