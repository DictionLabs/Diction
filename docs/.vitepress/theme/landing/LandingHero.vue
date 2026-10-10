<script setup lang="ts">
import HeroFigure from './HeroFigure.vue'
const APP_STORE = 'https://apps.apple.com/app/id6759807364'

// Media slot. Set `video` to a path like '/hero.mp4' to swap the placeholder photo
// for a muted looping clip. The photo is a Lorem Picsum placeholder until a real
// recording lands.
const media = {
  video: '' as string,
  // Phone recordings are portrait. Set portrait: true and the clip is centered in the
  // wide frame over a blurred copy of itself instead of being cropped.
  portrait: true,
  poster: '/placeholder/hero-wide.jpg',
}

</script>

<template>
  <section class="ld-section ink ld-grid-bg hero">
    <div class="ld-container">
      <h1 class="ld-h1 hero-h1" v-reveal="{ delay: 60 }">
        Say it. Send it.
      </h1>

      <div class="hero-row" v-reveal="{ delay: 140 }">
        <p class="ld-lead hero-lead">
          The intelligent voice keyboard for iPhone. Speak in any app and get text
          you can send as it is. Free on your iPhone and on your server, or use
          Diction Cloud.
        </p>
        <div class="hero-cta">
          <a class="ld-btn hero-btn-primary" :href="APP_STORE" target="_blank" rel="noopener">
            <img src="/apple-logo.svg" alt="" />
            Get the app
          </a>
          <a class="ld-btn ghost hero-btn-ghost" href="https://github.com/DictionLabs/Diction" target="_blank" rel="noopener">
            <img src="/github-mark.svg" alt="" />
            Self-host
          </a>
        </div>
      </div>

      <div class="hero-media" v-reveal="{ delay: 220 }">
        <template v-if="media.video">
          <video
            v-if="media.portrait"
            class="hero-media-el hero-media-blur"
            :src="media.video"
            autoplay
            muted
            loop
            playsinline
            aria-hidden="true"
          />
          <video
            class="hero-media-el"
            :class="{ portrait: media.portrait }"
            :src="media.video"
            :poster="media.poster"
            autoplay
            muted
            loop
            playsinline
          />
        </template>
        <HeroFigure v-else />
      </div>

    </div>
  </section>
</template>

<style scoped>
.hero {
  --hero-ink: var(--ld-navy-950);
  background-color: var(--hero-ink);
  padding-top: clamp(3.5rem, 8vw, 7rem);
  padding-bottom: clamp(3.5rem, 7vw, 6rem);
}


.hero-h1 {
  /* Four words, so it can run larger than the old two-line headline. */
  font-size: clamp(3.5rem, 11vw, 8.5rem);
  line-height: 0.98;
  letter-spacing: -0.035em;
  max-width: 22ch;
}

.hero-row {
  display: grid;
  grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr);
  gap: 2rem 4rem;
  align-items: end;
  margin-top: clamp(2rem, 4vw, 3rem);
}

.hero-lead {
  max-width: 560px;
  font-size: clamp(1.05rem, 1.5vw, 1.2rem);
}

.hero-cta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  justify-content: flex-end;
}
/* Scaled to sit under a display headline, not a body paragraph. */
.hero-cta .ld-btn {
  min-height: 62px;
  padding: 0 32px;
  font-size: 1.1875rem;
  font-weight: 600;
  gap: 12px;
}
.hero-cta .ld-btn img {
  width: 26px;
  height: 26px;
}

.hero-btn-primary {
  background: #fff;
  color: var(--ld-navy-950) !important;
}
.hero-btn-primary img {
  filter: brightness(0);
  /* The Apple glyph is narrow; at the GitHub mark's size it reads smaller. */
  width: 28px !important;
  height: 28px !important;
  margin-top: -3px;
}
.hero-btn-primary:hover {
  background: #e9e9ec;
}

.hero-btn-ghost {
  color: #fff !important;
  border-color: rgba(255, 255, 255, 0.28);
}
.hero-btn-ghost:hover {
  border-color: rgba(255, 255, 255, 0.6);
}

/* Media panel */
.hero-media {
  position: relative;
  margin-top: clamp(2.5rem, 6vw, 4.5rem);
  aspect-ratio: 21 / 9;
  border-radius: 16px;
  overflow: hidden;
  background: var(--ld-navy-900);
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.035) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.035) 1px, transparent 1px);
  background-size: 48px 48px;
  border: 1px solid rgba(255, 255, 255, 0.1);
}

.hero-media-el {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
  filter: saturate(0.85) contrast(1.05);
}

.hero-media-shade {
  position: absolute;
  inset: 0;
  background: linear-gradient(
    to top,
    color-mix(in srgb, var(--ld-navy-950) 90%, transparent) 0%,
    color-mix(in srgb, var(--ld-navy-950) 18%, transparent) 45%,
    transparent 100%
  );
  pointer-events: none;
}

.hero-media-tag {
  position: absolute;
  top: 14px;
  left: 16px;
  padding: 6px 10px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--ld-navy-900) 66%, transparent);
  color: rgba(255, 255, 255, 0.75);
  backdrop-filter: blur(6px);
}

.hero-media-el.portrait {
  object-fit: contain;
  z-index: 1;
}
.hero-media-blur {
  filter: blur(28px) brightness(0.55) saturate(0.8);
  transform: scale(1.15);
}
.hero-btn-ghost img {
  filter: brightness(0) invert(1);
}

/* Above the phone breakpoint the headline's second line never breaks. */
@media (min-width: 641px) {
  .hero-h1-line2 {
    white-space: nowrap;
  }
}

@media (max-width: 900px) {
  .hero-row {
    grid-template-columns: minmax(0, 1fr);
  }
  .hero-cta {
    justify-content: flex-start;
  }
}

@media (max-width: 640px) {
  .hero-h1 {
    font-size: clamp(2.6rem, 12.5vw, 3.4rem);
  }
  .hero-media {
    aspect-ratio: 4 / 5;
  }
  .hero-cta .ld-btn {
    width: 100%;
    min-height: 56px;
    font-size: 1.0625rem;
  }
}
</style>
