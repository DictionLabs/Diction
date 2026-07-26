<script setup>
import { useData } from 'vitepress'

const { isDark } = useData()

const lightImages = [
  '/keyboard/light/IMG_4473.PNG',
  '/keyboard/light/IMG_4475.PNG',
  '/keyboard/light/IMG_4476.PNG',
  '/keyboard/light/IMG_4477.PNG',
  '/keyboard/light/IMG_4478.PNG',
  '/keyboard/light/IMG_4482.PNG',
]

const darkImages = [
  '/keyboard/dark/IMG_4469.PNG',
  '/keyboard/dark/IMG_4470.PNG',
  '/keyboard/dark/IMG_4471.PNG',
  '/keyboard/dark/IMG_4472.PNG',
  '/keyboard/dark/IMG_4479.PNG',
  '/keyboard/dark/IMG_4480.PNG',
  '/keyboard/dark/IMG_4481.PNG',
]
</script>

<template>
  <section class="keyboard-themes">
    <div class="keyboard-themes-inner">
      <h2 class="keyboard-themes-heading">Keyboard themes</h2>
      <p class="keyboard-themes-sub">Make it yours. Pick from built-in themes.</p>
      <div class="keyboard-themes-scroll">
        <template v-if="!isDark">
          <img
            v-for="src in lightImages"
            :key="src"
            :src="src"
            alt="Diction keyboard light theme"
            class="keyboard-theme-img"
            loading="lazy"
          />
        </template>
        <template v-else>
          <img
            v-for="src in darkImages"
            :key="src"
            :src="src"
            alt="Diction keyboard dark theme"
            class="keyboard-theme-img"
            loading="lazy"
          />
        </template>
      </div>
    </div>
  </section>
</template>

<style scoped>
.keyboard-themes {
  padding: 5rem 0 6rem;
  overflow: hidden;
}

.keyboard-themes-inner {
  text-align: center;
}

.keyboard-themes-heading {
  font-family: 'FiraSans', sans-serif;
  font-weight: 400;
  font-style: italic;
  font-size: clamp(1.75rem, 3.5vw, 2.25rem);
  color: var(--vp-c-text-1);
  margin: 0 0 0.5rem;
  border: none;
  letter-spacing: normal;
}

.keyboard-themes-sub {
  font-size: 1rem;
  color: var(--vp-c-text-2);
  margin: 0 0 2.5rem;
}

.keyboard-themes-scroll {
  display: flex;
  gap: 1.25rem;
  overflow-x: auto;
  padding: 0.5rem 1.5rem 1.5rem;
  scroll-snap-type: x mandatory;
  -webkit-overflow-scrolling: touch;
  scrollbar-width: none;
}

.keyboard-themes-scroll::-webkit-scrollbar {
  display: none;
}

.keyboard-theme-img {
  flex: 0 0 auto;
  width: 280px;
  height: auto;
  border-radius: 16px;
  scroll-snap-align: start;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.08);
}

.dark .keyboard-theme-img {
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.4);
}

@media (max-width: 640px) {
  .keyboard-theme-img {
    width: 220px;
  }
}
</style>
