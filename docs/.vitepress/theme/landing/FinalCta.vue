<script setup lang="ts">
const APP_STORE_URL = 'https://apps.apple.com/app/id6759807364'
</script>

<template>
  <section class="ld-section final-cta">
    <div class="ld-container ld-center">
      <div class="cta-copy ld-stagger" v-reveal>
        <h2 class="ld-h1 cta-h1">Your keyboard is waiting.</h2>
        <p class="ld-lead cta-lead">
          Free to start. Free forever if you host it. Every app on your iPhone.
        </p>
        <div class="ld-actions cta-actions">
          <a class="ld-btn brand" :href="APP_STORE_URL" target="_blank" rel="noopener">
            <img src="/apple-logo.svg" alt="" />
            Get the app
          </a>
          <a class="ld-btn alt" href="/self-hosted">
            <img src="/github-mark.svg" alt="" />
            Self-host
          </a>
        </div>
      </div>

    </div>
  </section>
</template>

<style scoped>
.final-cta {
  position: relative;
  overflow: hidden;
}

.cta-copy {
  position: relative;
  z-index: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
}

.cta-h1 {
  font-size: clamp(2.5rem, 7vw, 5.5rem);
}

.cta-lead {
  margin-top: 1.5rem;
  font-size: clamp(1.05rem, 1.8vw, 1.3rem);
}

.cta-actions {
  margin-top: 2.25rem;
  justify-content: center;
}

.cta-chips {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 10px;
  margin-top: 2.5rem;
}

</style>
