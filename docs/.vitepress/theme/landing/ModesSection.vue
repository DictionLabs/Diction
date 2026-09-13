<script setup lang="ts">
interface Logo {
  /** Path under /public, rendered through a CSS mask tinted with currentColor. */
  icon?: string
  /** Text tile instead of an icon tile. */
  text?: string
  /** Render the text tile in the Diction wordmark face. */
  wordmark?: boolean
  label: string
}

interface Chip {
  text: string
  violet?: boolean
}

interface Plan {
  key: string
  index: string
  name: string
  price: string
  priceNote?: string
  promise: string
  logos: Logo[]
  /** Compact mono list (card 1: the on-device models). */
  list?: string[]
  listLabel?: string
  /** Mono chips (cards 2 and 3). Blue or neutral = speech, violet = Writing Tools. */
  chips?: Chip[]
  cta: string
  href: string
  featured?: boolean
}

const plans: Plan[] = [
  {
    key: 'device',
    index: '01 / On-device',
    name: 'On your iPhone',
    price: 'Free',
    promise: 'Speech models run on the phone. Nothing leaves it.',
    logos: [
      { icon: '/icon-nvidia.svg', label: 'NVIDIA' },
      { text: 'Whisper', label: 'Whisper' },
    ],
    listLabel: 'On-device models',
    list: ['Whisper Base', 'Whisper Small', 'Whisper Turbo', 'NVIDIA Parakeet v2', 'NVIDIA Parakeet v3'],
    cta: 'On-device',
    href: '/on-device',
  },
  {
    key: 'server',
    index: '02 / Self-hosted',
    name: 'On your server',
    price: 'Free',
    promise: 'Run your own speech model or LLM? Plug it into the keyboard.',
    logos: [
      { icon: '/github-mark.svg', label: 'GitHub' },
      { icon: '/icon-docker.svg', label: 'Docker' },
    ],
    chips: [{ text: 'open source' }, { text: 'docker compose up' }, { text: 'your STT' }, { text: 'your LLM' }],
    cta: 'Self-hosting guide',
    href: '/features/self-hosting-setup',
  },
  {
    key: 'cloud',
    index: '03 / Cloud',
    name: 'Diction One',
    price: 'Subscription',
    priceNote: 'free trial included',
    promise: 'Our servers, our most accurate models, zero setup.',
    logos: [
      { icon: '/icon-cloud.svg', label: 'Cloud' },
      { text: 'Diction', wordmark: true, label: 'Diction' },
    ],
    chips: [{ text: 'most accurate' }, { text: 'live transcription' }, { text: 'writing tools', violet: true }],
    cta: 'Start free trial',
    href: 'https://apps.apple.com/app/id6759807364',
    featured: true,
  },
]
</script>

<template>
  <section class="ld-section ld-grid-bg modes-section">
    <div class="ld-container">
      <div class="ld-label-row" v-reveal>
        <span class="ld-mono">04 / Where it runs</span>
        <span class="ld-mono">diction.one</span>
      </div>

      <div class="modes-head" v-reveal="{ delay: 60 }">
        <h2 class="ld-h2">Three ways to run it. Two are free.</h2>
        <p class="ld-lead">Pick where your voice is processed. Change it any time.</p>
      </div>

      <div class="modes-grid ld-stagger" v-reveal="{ delay: 120 }">
        <article
          v-for="p in plans"
          :key="p.key"
          class="ld-card hover plan"
          :class="{ featured: p.featured }"
        >
          <p class="ld-mono plan-index">{{ p.index }}</p>
          <h3 class="plan-name">{{ p.name }}</h3>

          <div class="plan-price">
            <span class="plan-price-value">{{ p.price }}</span>
            <span v-if="p.priceNote" class="plan-price-note">{{ p.priceNote }}</span>
          </div>

          <p class="plan-promise">{{ p.promise }}</p>

          <div class="plan-logos" role="list">
            <span
              v-for="l in p.logos"
              :key="l.label"
              class="plan-tile"
              :class="{ 'is-text': l.text, 'is-wordmark': l.wordmark }"
              role="listitem"
              :aria-label="l.label"
            >
              <span v-if="l.icon" class="plan-tile-icon" :style="{ '--icon': `url('${l.icon}')` }" aria-hidden="true"></span>
              <span v-else class="plan-tile-text" :class="{ 'ld-wordmark': l.wordmark }">{{ l.text }}</span>
            </span>
          </div>

          <div v-if="p.list" class="plan-stack">
            <p v-if="p.listLabel" class="plan-stack-label">{{ p.listLabel }}</p>
            <ul class="plan-list">
              <li v-for="m in p.list" :key="m" class="plan-list-item">{{ m }}</li>
            </ul>
          </div>

          <div v-if="p.chips" class="plan-stack">
            <ul class="plan-chips">
              <li
                v-for="c in p.chips"
                :key="c.text"
                class="plan-chip"
                :class="{ 'ld-chip': c.violet, violet: c.violet }"
              >
                {{ c.text }}
              </li>
            </ul>
          </div>

          <a
            :href="p.href"
            class="ld-btn plan-btn"
            :class="p.featured ? 'brand' : 'ghost'"
            :target="p.featured ? '_blank' : undefined"
            :rel="p.featured ? 'noopener' : undefined"
          >
            <img v-if="p.featured" src="/apple-logo.svg" alt="" />
            {{ p.cta }}
          </a>
        </article>
      </div>

      <p class="ld-small modes-foot" v-reveal="{ delay: 160 }">
        Same keyboard in all three. Switch any time.
      </p>
    </div>
  </section>
</template>

<style scoped>
.modes-section {
  background-color: var(--vp-c-bg);
}

/* ---- Header: headline left, lead right ---- */
.modes-head {
  display: grid;
  grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr);
  align-items: end;
  gap: 1.5rem clamp(2rem, 5vw, 5rem);
  margin-bottom: clamp(2.5rem, 5vw, 4rem);
}
.modes-head .ld-lead {
  max-width: 420px;
  justify-self: end;
}

/* ---- Grid of plans ---- */
.modes-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1rem;
  align-items: stretch;
}

/* Per-card palette: the featured card flips to ink in light mode and to paper in dark mode */
.plan {
  --pl-bg: var(--ld-surface);
  --pl-fg: var(--vp-c-text-1);
  --pl-fg2: var(--vp-c-text-2);
  --pl-fg3: var(--vp-c-text-3);
  --pl-line: var(--vp-c-divider);
  --pl-tile: var(--ld-surface-2);
  position: relative;
  display: flex;
  flex-direction: column;
  padding: clamp(1.5rem, 2.2vw, 2rem);
  background: var(--pl-bg);
  color: var(--pl-fg);
}
.plan.featured {
  --pl-bg: #16171a;
  --pl-fg: #ffffff;
  --pl-fg2: rgba(255, 255, 255, 0.74);
  --pl-fg3: rgba(255, 255, 255, 0.5);
  --pl-line: rgba(255, 255, 255, 0.14);
  --pl-tile: rgba(255, 255, 255, 0.09);
  border-color: var(--vp-c-brand-1);
  box-shadow: var(--ld-shadow-md), 0 0 0 1px var(--vp-c-brand-1);
}
.dark .plan.featured {
  --pl-bg: #f4f4f5;
  --pl-fg: #111113;
  --pl-fg2: rgba(0, 0, 0, 0.7);
  --pl-fg3: rgba(0, 0, 0, 0.48);
  --pl-line: rgba(0, 0, 0, 0.12);
  --pl-tile: rgba(0, 0, 0, 0.06);
}

/* ---- Name, price, promise ---- */
.plan-index {
  color: var(--pl-fg3);
  margin-bottom: 0.5rem;
}
.plan-name {
  font-size: clamp(1.35rem, 1.8vw, 1.6rem);
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: 1.15;
  color: var(--pl-fg);
}
.plan-price {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 0.25rem 0.75rem;
  margin-top: 1rem;
}
.plan-price-value {
  font-size: clamp(1.75rem, 2.4vw, 2.125rem);
  font-weight: 700;
  letter-spacing: -0.035em;
  line-height: 1;
  color: var(--pl-fg);
}
.plan-price-note {
  font-family: var(--vp-font-family-mono);
  font-size: 0.6875rem;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  line-height: 1.4;
  color: var(--pl-fg3);
}
.plan-promise {
  margin-top: 0.75rem;
  font-size: 1rem;
  line-height: 1.5;
  color: var(--pl-fg2);
  text-wrap: pretty;
}

/* ---- Logo row ---- */
.plan-logos {
  display: flex;
  gap: 0.5rem;
  margin-top: 1.5rem;
  padding-top: 1.25rem;
  border-top: 1px solid var(--pl-line);
}
.plan-tile {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 56px;
  min-width: 56px;
  padding: 0 14px;
  border-radius: 14px;
  background: var(--pl-tile);
  color: var(--pl-fg);
}
.plan-tile-icon {
  display: block;
  width: 28px;
  height: 28px;
  background: currentColor;
  -webkit-mask: var(--icon) center / contain no-repeat;
  mask: var(--icon) center / contain no-repeat;
}
.plan-tile-text {
  font-size: 1.0625rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: 1;
}
.plan-tile-text.ld-wordmark {
  font-size: 1.35rem;
  letter-spacing: 0.5px;
}

/* ---- Stack: mono model list or mono chips ---- */
.plan-stack {
  margin-top: 1.25rem;
  margin-bottom: 1.75rem;
}
.plan-stack-label {
  font-family: var(--vp-font-family-mono);
  font-size: 0.6875rem;
  font-weight: 500;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--pl-fg3);
  margin-bottom: 0.5rem;
}
.plan-list {
  display: flex;
  flex-direction: column;
}
.plan-list-item {
  font-family: var(--vp-font-family-mono);
  font-size: 0.8125rem;
  letter-spacing: 0.01em;
  line-height: 1.3;
  color: var(--pl-fg);
  padding: 0.45rem 0;
  border-top: 1px solid var(--pl-line);
}
.plan-list-item:last-child {
  border-bottom: 1px solid var(--pl-line);
}
.plan-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.plan-chip {
  display: inline-flex;
  align-items: center;
  min-height: 30px;
  padding: 0 11px;
  border-radius: 8px;
  border: 1px solid var(--pl-line);
  font-family: var(--vp-font-family-mono);
  font-size: 0.8125rem;
  letter-spacing: 0.01em;
  color: var(--pl-fg);
  white-space: nowrap;
}
.plan.featured .plan-chip {
  border-color: transparent;
  background: var(--pl-tile);
}
/* Writing Tools chip: violet, on top of the card palette (specificity matches the featured rule, so it must come after) */
.plan .plan-chip.violet {
  border-color: transparent;
  background: var(--ld-violet-soft);
  color: var(--ld-violet);
}
/* The featured card inverts against the page theme, so it takes the other theme's violet values */
.plan.featured {
  --ld-violet: #bf5af2;
  --ld-violet-soft: rgba(191, 90, 242, 0.18);
}
.dark .plan.featured {
  --ld-violet: #af52de;
  --ld-violet-soft: rgba(175, 82, 222, 0.14);
}

/* ---- Button pinned to the bottom ---- */
.plan-btn {
  width: 100%;
  margin-top: auto;
}
.plan-btn.ghost {
  color: var(--pl-fg) !important;
  border-color: var(--pl-line);
}
.plan-btn.ghost:hover {
  border-color: var(--pl-fg3);
}

/* ---- Footer line ---- */
.modes-foot {
  margin-top: clamp(2rem, 4vw, 3rem);
  padding-top: 1rem;
  border-top: 1px solid var(--vp-c-divider);
  color: var(--vp-c-text-2);
}

/* ---- Tablet ---- */
@media (max-width: 960px) {
  .modes-grid {
    grid-template-columns: 1fr;
    max-width: 560px;
    margin-inline: auto;
  }
  .modes-head {
    grid-template-columns: 1fr;
  }
  .modes-head .ld-lead {
    justify-self: start;
  }
}

/* ---- Phone ---- */
@media (max-width: 640px) {
  .plan {
    padding: 1.5rem 1.25rem;
  }
}
</style>
