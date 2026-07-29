<script setup>
import { useData } from 'vitepress'
import { computed } from 'vue'

const { isDark } = useData()
const suffix = computed(() => (isDark.value ? 'dark' : 'light'))

const steps = [
  { video: 'hero-welcome', title: 'Type and 5x faster with your voice', radius: 30 },
  { video: 'switch-keyboard-loop', title: 'Switch keyboard and record', radius: 40 },
  { video: 'sandbox-edit-loop', title: 'Hold for edit mode', radius: 40 },
]
</script>

<template>
  <section class="how-it-works">
    <div class="how-it-works-inner">
      <h2 class="how-it-works-heading">How it works</h2>
      <div class="how-it-works-grid">
        <div v-for="step in steps" :key="step.video" class="how-it-works-step">
          <div class="how-it-works-video-frame" :style="{ borderRadius: `0 0 ${step.radius}px ${step.radius}px` }">
            <video
              :src="`/${step.video}-${suffix}.mp4`"
              autoplay
              muted
              loop
              playsinline
              preload="auto"
              class="how-it-works-video"
            />
          </div>
          <h3 class="how-it-works-title">{{ step.title }}</h3>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.how-it-works {
  padding: 5rem 1.5rem;
}

.how-it-works-inner {
  max-width: 1152px;
  margin: 0 auto;
  text-align: center;
}

.how-it-works-heading {
  font-family: 'FiraSans', sans-serif;
  font-weight: 400;
  font-style: italic;
  font-size: clamp(1.75rem, 3.5vw, 2.25rem);
  color: var(--vp-c-text-1);
  margin: 0 0 3rem;
  border: none;
  letter-spacing: normal;
}

.how-it-works-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 2.5rem;
}

.how-it-works-step {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  gap: 1.5rem;
}

.how-it-works-video-frame {
  width: 100%;
  max-width: 260px;
  border-radius: 0 0 30px 30px;
  overflow: hidden;
  border: 2.5px solid color-mix(in srgb, var(--vp-c-text-3) 40%, transparent);
  border-top: none;
  -webkit-mask-image: linear-gradient(to bottom, transparent 0%, #fff 30%, #fff 100%);
  mask-image: linear-gradient(to bottom, transparent 0%, #fff 30%, #fff 100%);
}

.how-it-works-video {
  display: block;
  width: 100%;
  height: auto;
}

.how-it-works-title {
  font-family: 'FiraSans', sans-serif;
  font-weight: 200;
  font-style: italic;
  font-size: 1.5rem;
  line-height: 1.3;
  color: var(--vp-c-text-1);
  margin: 0;
  border: none;
  max-width: 280px;
}

@media (max-width: 768px) {
  .how-it-works-grid {
    grid-template-columns: 1fr;
    gap: 3rem;
  }
}
</style>
