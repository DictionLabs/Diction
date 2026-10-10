<script setup lang="ts">
// Diction One: the paid cloud plan, with its two prices. The cards copy the
// app's plan screen (ios/Diction/Subscription/SubscriptionView.swift): both
// priced per month, annual rounded down like the app does, "billed annually",
// "2 months free". Prices are the US App Store prices (checked 2026-09-19);
// other storefronts differ, which the note under the cards says.

const APP_STORE = 'https://apps.apple.com/app/id6759807364'

// Short on purpose: one clean signal per line.
const features = ['Live transcription', 'Every Writing Tool', 'No word limits', 'Audio never stored']

interface Price {
  key: string
  name: string
  amount: string
  period: string
  billed?: string
  badge?: string
}

const prices: Price[] = [
  { key: 'monthly', name: 'Monthly', amount: '$5.99', period: '/month' },
  {
    key: 'annual',
    name: 'Annual',
    amount: '$4.99',
    period: '/month',
    billed: '$59.99 billed annually',
    badge: '2 months free',
  },
]
</script>

<template>
  <section id="pricing" class="ld-section soft one">
    <div class="ld-container">

      <div class="one-grid">
        <div class="one-text" v-reveal>
          <h2 class="ld-h2 big in-col">Diction Cloud.<br />Hard to beat.</h2>
          <p class="ld-lead">
            We run the GPU servers ourselves. Your voice is transcribed there and thrown away, never
            stored. Running the hardware also keeps costs down, which is how the price stays fair and
            below what most dictation apps charge.
          </p>
          <ul class="one-list">
            <li v-for="f in features" :key="f">
              <span class="ld-check" aria-hidden="true"></span>
              <span>{{ f }}</span>
            </li>
          </ul>
        </div>

        <div class="one-plans" v-reveal="{ delay: 120 }">
          <div class="one-prices">
            <div v-for="p in prices" :key="p.key" class="ld-card one-price" :class="{ best: p.badge }">
              <div class="one-price-head">
                <span class="one-price-name">{{ p.name }}</span>
                <span v-if="p.badge" class="one-badge">{{ p.badge }}</span>
              </div>
              <p class="one-amount">
                <b>{{ p.amount }}</b>
                <span>{{ p.period }}</span>
              </p>
              <p class="one-note">{{ p.billed ?? '\u00a0' }}</p>
            </div>
          </div>

          <a class="ld-btn brand one-cta" :href="APP_STORE" target="_blank" rel="noopener">
            <img src="/apple-logo.svg" alt="" />
            Get the app
          </a>
          <p class="one-fine">Free trial in the app. Cancel anytime. US prices, your local price is in the app.</p>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
/* The nav's "Pricing" link lands here; keep the heading clear of the fixed bar. */
#pricing {
  scroll-margin-top: var(--vp-nav-height);
}
.one-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: clamp(2rem, 5vw, 4.5rem);
  align-items: center;
}

.one-text .ld-h2 {
  margin-bottom: 1rem;
}
.one-list {
  display: flex;
  flex-direction: column;
  gap: 0.65rem;
  margin-top: 1.5rem;
  padding-top: 1.25rem;
  border-top: 1px solid var(--vp-c-divider);
}
.one-list li {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 0;
  font-size: 0.9375rem;
  font-weight: 500;
  line-height: 1.4;
  color: var(--vp-c-text-1);
}
.one-list .ld-check {
  background-color: var(--ld-violet);
}

/* ---- Price cards ---- */
.one-prices {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1rem;
}
.one-price {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 1.5rem 1.4rem;
}
.one-price.best {
  border-color: var(--ld-violet);
  box-shadow: var(--ld-shadow-md), 0 0 0 1px var(--ld-violet);
}
.one-price-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.one-price-name {
  font-size: 1rem;
  font-weight: 600;
  color: var(--vp-c-text-1);
}
.one-badge {
  padding: 3px 9px;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--ld-violet);
  background: var(--ld-violet-soft);
  white-space: nowrap;
}
.one-amount {
  display: flex;
  align-items: baseline;
  gap: 6px;
  margin: 0.25rem 0 0;
}
.one-amount b {
  font-size: clamp(2rem, 3.4vw, 2.5rem);
  font-weight: 700;
  letter-spacing: -0.03em;
  line-height: 1;
  color: var(--vp-c-text-1);
}
.one-amount span {
  font-size: 0.9375rem;
  color: var(--vp-c-text-2);
}
.one-note {
  margin: 0;
  font-size: 0.875rem;
  color: var(--vp-c-text-2);
}

.one-cta {
  width: 100%;
  margin-top: 1.25rem;
  min-height: 54px;
  font-size: 1rem;
}
.one-fine {
  margin: 0.75rem 0 0;
  text-align: center;
  font-size: 0.8125rem;
  color: var(--vp-c-text-3);
}

@media (max-width: 860px) {
  .one-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (max-width: 420px) {
  .one-prices {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
