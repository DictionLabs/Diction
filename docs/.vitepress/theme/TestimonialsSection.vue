<script setup lang="ts">
import { ref, computed } from 'vue'
import { data as reviews } from './reviews.data.js'

const visibleReviews = computed(() => reviews.slice(0, 4))
const APP_STORE_REVIEWS_URL = 'https://apps.apple.com/app/id6759807364?see-all=reviews'

const expanded = ref(new Set())
function toggle(event: Event, author: string) {
  event.preventDefault()
  event.stopPropagation()
  if (expanded.value.has(author)) expanded.value.delete(author)
  else expanded.value.add(author)
}
</script>

<template>
  <section v-if="visibleReviews.length > 0" class="testimonials">
    <div class="testimonials-inner">
      <p class="testimonials-label">From the App Store</p>
      <h2 class="testimonials-heading">Straight from real users.</h2>
      <div class="testimonials-grid">
        <a
          v-for="review in visibleReviews"
          :key="review.author"
          :href="APP_STORE_REVIEWS_URL"
          target="_blank"
          rel="noopener"
          class="testimonial-card"
        >
          <div class="stars">&#9733;&#9733;&#9733;&#9733;&#9733;</div>
          <span class="review-title">{{ review.title }}</span>
          <p class="review-body" :class="{ clamped: !expanded.has(review.author) }">{{ review.body }}</p>
          <button
            v-if="review.body.length > 280"
            class="review-toggle"
            @click="toggle($event, review.author)"
          >
            {{ expanded.has(review.author) ? 'Show less' : 'Read more' }}
          </button>
          <div class="review-footer">
            <Icon name="user-circle" class="review-author-icon" />
            <span class="review-author">{{ review.author }}</span>
          </div>
        </a>
      </div>
    </div>
  </section>
</template>

<style scoped>
.testimonials {
  padding: 5rem 1.5rem;
  background: var(--vp-c-bg-soft);
}

.testimonials-inner {
  max-width: 1152px;
  margin: 0 auto;
  text-align: center;
}

.testimonials-label {
  font-size: 0.8rem;
  font-weight: 600;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--vp-c-text-3);
  margin: 0 0 0.75rem;
}

.testimonials-heading {
  font-family: 'FiraSans', sans-serif;
  font-weight: 400;
  font-style: italic;
  font-size: clamp(1.75rem, 3.5vw, 2.25rem);
  color: var(--vp-c-text-1);
  margin: 0 0 3rem;
  border: none;
  letter-spacing: normal;
}

.testimonials-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 1.25rem;
  text-align: left;
}

.testimonial-card {
  background: var(--vp-c-bg);
  border: 1px solid var(--vp-c-divider);
  border-radius: 14px;
  padding: 1.5rem;
  display: flex;
  flex-direction: column;
  gap: 0.875rem;
  transition: box-shadow 0.2s ease, transform 0.2s ease;
  text-decoration: none;
  color: inherit;
}

.testimonial-card:hover {
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.08);
  transform: translateY(-2px);
}

.dark .testimonial-card:hover {
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.3);
}

.stars {
  color: #FF9F0A;
  font-size: 1rem;
  letter-spacing: 0.1em;
}

.review-body {
  font-size: 0.95rem;
  color: var(--vp-c-text-2);
  line-height: 1.65;
  margin: 0;
  flex: 1;
  white-space: pre-line;
}

.review-body.clamped {
  display: -webkit-box;
  -webkit-line-clamp: 6;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.review-toggle {
  align-self: flex-start;
  background: none;
  border: none;
  padding: 0;
  margin: -0.5rem 0 0;
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--vp-c-brand-1);
  cursor: pointer;
}

.review-toggle:hover {
  text-decoration: underline;
}

.review-footer {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding-top: 0.5rem;
  border-top: 1px solid var(--vp-c-divider);
}

.review-author-icon {
  color: var(--vp-c-text-3);
  font-size: 1.1em;
  flex-shrink: 0;
}

.review-title {
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--vp-c-text-1);
}

.review-author {
  font-size: 0.8rem;
  color: var(--vp-c-text-3);
}

@media (max-width: 640px) {
  .testimonials {
    padding: 3.5rem 1.25rem;
  }
}
</style>
