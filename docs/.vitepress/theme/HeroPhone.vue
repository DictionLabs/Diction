<script setup>
import HeroDecorations from './HeroDecorations.vue'
</script>

<template>
  <div class="hero-phone-outer">
    <HeroDecorations />
    <div class="hero-phone-wrapper">
      <img src="/hero-phone-3d.png" alt="Diction 3D phone" class="hero-phone" draggable="false" />
    </div>
  </div>
</template>

<style scoped>
.hero-phone-outer {
  position: relative;
  width: 100%;
  max-width: 680px;
  aspect-ratio: 1 / 1;
  margin: 0 auto;
  display: flex;
  align-items: center;
  justify-content: center;
}

.hero-phone-wrapper {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  isolation: isolate;
  z-index: 1;
}

.hero-phone-wrapper::before {
  content: "";
  position: absolute;
  z-index: -1;
  inset: -60px;
  background:
    radial-gradient(
      ellipse at center,
      rgba(120, 120, 120, 0.12) 0%,
      rgba(120, 120, 120, 0.06) 35%,
      rgba(120, 120, 120, 0) 70%
    );
  filter: blur(30px);
  pointer-events: none;
}

.hero-phone {
  width: 420px;
  height: auto;
  display: block;
  filter: drop-shadow(0 0 20px rgba(120, 120, 120, 0.15));
}

:root.dark .hero-phone-wrapper::before {
  background:
    radial-gradient(
      ellipse at center,
      rgba(200, 200, 200, 0.06) 0%,
      rgba(200, 200, 200, 0.03) 35%,
      rgba(200, 200, 200, 0) 70%
    );
}

:root.dark .hero-phone {
  filter: drop-shadow(0 0 12px rgba(200, 200, 200, 0.08));
}

@media (max-width: 1279px) {
  .hero-phone {
    width: 360px;
  }
}

@media (max-width: 767px) {
  .hero-phone {
    width: 280px;
  }
  .hero-phone-wrapper::before {
    inset: -40px;
  }
}

@media (max-width: 479px) {
  .hero-phone {
    width: 220px;
  }
}
</style>
