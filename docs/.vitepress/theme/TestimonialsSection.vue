<script setup lang="ts">
import reviews from './reviews-curated.json'

interface CuratedReview {
  author: string
  title: string
  excerpt: string
  translated?: string
  storefront: string
}

const APP_STORE_REVIEWS_URL = 'https://apps.apple.com/app/id6759807364?see-all=reviews'

const list = reviews as CuratedReview[]
// First entry is the featured quote; the rest fill the grid.
const featured = list[0]
const others = list.slice(1)

function initial(author: string): string {
  return (author.trim().charAt(0) || 'A').toUpperCase()
}

// Short untranslated titles become the overline; translated reviews show the language note instead.
function overline(review: CuratedReview): string | null {
  if (review.translated) return `Translated from ${review.translated}`
  const title = review.title.trim().replace(/[.!]+$/, '')
  return title.length > 0 && title.length <= 32 ? title : null
}
</script>

<template>
  <section v-if="list.length > 0" class="ld-section testimonials">
    <div class="ld-container">
      <div class="ld-label-row" v-reveal>
        <span class="ld-mono">08 / From the App Store</span>
        <span class="ld-mono">apps.apple.com</span>
      </div>

      <div class="t-head" v-reveal>
        <h2 class="ld-h2">People who switched.</h2>
        <a :href="APP_STORE_REVIEWS_URL" target="_blank" rel="noopener" class="rating">
          <span class="stars" aria-hidden="true">&#9733;&#9733;&#9733;&#9733;&#9733;</span>
          <span class="ld-readout"><b>4.9</b> on the App Store</span>
        </a>
      </div>

      <div class="t-layout">
        <a
          v-if="featured"
          :href="APP_STORE_REVIEWS_URL"
          target="_blank"
          rel="noopener"
          class="ld-card hover t-card featured"
          v-reveal
        >
          <span class="quote-mark" aria-hidden="true">&ldquo;</span>
          <span v-if="overline(featured)" class="overline ld-mono">{{ overline(featured) }}</span>
          <blockquote class="quote">&ldquo;{{ featured.excerpt }}&rdquo;</blockquote>
          <div class="author">
            <span class="avatar" aria-hidden="true">{{ initial(featured.author) }}</span>
            <span class="name">{{ featured.author }}</span>
            <span class="source ld-mono">App Store &middot; {{ featured.storefront }}</span>
          </div>
        </a>

        <div class="t-grid ld-stagger" v-reveal="{ delay: 120 }">
          <a
            v-for="review in others"
            :key="review.author"
            :href="APP_STORE_REVIEWS_URL"
            target="_blank"
            rel="noopener"
            class="ld-card hover t-card"
          >
            <span v-if="overline(review)" class="overline ld-mono">{{ overline(review) }}</span>
            <blockquote class="quote">&ldquo;{{ review.excerpt }}&rdquo;</blockquote>
            <div class="author">
              <span class="avatar" aria-hidden="true">{{ initial(review.author) }}</span>
              <span class="name">{{ review.author }}</span>
              <span class="source ld-mono">App Store &middot; {{ review.storefront }}</span>
            </div>
          </a>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
/* ---------- Header ---------- */
.t-head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 1rem 2rem;
  margin-bottom: clamp(2rem, 4vw, 3rem);
}
.rating {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  min-height: 44px;
  padding: 0 14px;
  border: 1px solid var(--vp-c-divider);
  border-radius: 10px;
  color: var(--vp-c-text-2);
  text-decoration: none !important;
  transition: border-color 0.2s;
}
.rating:hover {
  border-color: var(--vp-c-text-3);
}
.stars {
  color: var(--ld-violet-blue);
  font-size: 0.9375rem;
  letter-spacing: 0.08em;
  line-height: 1;
}

/* ---------- Layout ---------- */
.t-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 1rem;
  align-items: start;
}
.t-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 1rem;
}
@media (min-width: 640px) {
  .t-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (min-width: 960px) {
  .t-layout {
    grid-template-columns: minmax(0, 5fr) minmax(0, 7fr);
    gap: 1.25rem;
  }
  .t-grid {
    gap: 1.25rem;
  }
}

/* ---------- Cards ---------- */
.t-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding: 1.375rem 1.375rem 1.25rem;
  color: inherit;
  text-decoration: none !important;
}
.overline {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 1.5;
}
.quote {
  margin: 0;
  padding: 0;
  border: none;
  font-size: 0.9375rem;
  line-height: 1.6;
  color: var(--vp-c-text-1);
  text-wrap: pretty;
}

/* ---------- Featured quote block ---------- */
.featured {
  padding: clamp(1.75rem, 3vw, 2.5rem) clamp(1.375rem, 3vw, 2.25rem) 1.5rem;
  gap: 1rem;
  overflow: hidden;
}
.featured .quote-mark {
  position: absolute;
  top: 0.9rem;
  left: 0.75rem;
  font-size: clamp(8rem, 12vw, 10rem);
  font-weight: 700;
  line-height: 1;
  color: var(--vp-c-brand-1);
  opacity: 0.14;
  pointer-events: none;
  user-select: none;
}
@media (min-width: 960px) {
  .featured {
    position: sticky;
    top: calc(var(--vp-nav-height, 64px) + 24px);
  }
}
.featured .quote {
  font-size: clamp(1.25rem, 2vw, 1.5rem);
  font-weight: 600;
  line-height: 1.4;
  letter-spacing: -0.015em;
  text-wrap: balance;
}
.featured .overline,
.featured .quote {
  position: relative;
}

/* ---------- Author row ---------- */
.author {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: auto;
  padding-top: 0.875rem;
  border-top: 1px solid var(--vp-c-divider);
}
.avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  border-radius: 50%;
  background: var(--vp-c-brand-soft);
  color: var(--vp-c-brand-1);
  font-size: 0.8125rem;
  font-weight: 700;
  line-height: 1;
}
.name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--vp-c-text-1);
}
.source {
  margin-left: auto;
  flex: 0 0 auto;
  font-size: 0.6875rem;
}
</style>
