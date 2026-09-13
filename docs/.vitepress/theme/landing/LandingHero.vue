<script setup lang="ts">
import HeroScript from './HeroScript.vue'

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

const readouts = [
  { k: 'Runs on', v: 'iPhone / your server / our cloud' },
  { k: 'Writing Tools', v: 'fillers, grammar, formatting' },
  { k: 'Server', v: 'open source' },
]

</script>

<template>
  <section class="ld-section ink ld-grid-bg hero">
    <div class="ld-container">
      <div class="hero-top" v-reveal>
        <span class="ld-mono">Diction Labs</span>
        <span class="ld-mono hero-top-right">Keyboard for iPhone</span>
      </div>

      <h1 class="ld-h1 hero-h1" v-reveal="{ delay: 60 }">
        The intelligent keyboard for iPhone.
      </h1>

      <div class="hero-row" v-reveal="{ delay: 140 }">
        <p class="ld-lead hero-lead">
          It types with an autocorrect I wrote from scratch. It turns what you say into
          clean text, fast. It rewrites what is already on screen when you ask. And you
          decide whether any of that ever leaves your phone.
        </p>
        <div class="hero-cta">
          <a class="ld-btn hero-btn-primary" :href="APP_STORE" target="_blank" rel="noopener">
            <img src="/apple-logo.svg" alt="" />
            Get the app
          </a>
          <a class="ld-btn ghost hero-btn-ghost" href="/self-hosted">
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
        <img v-else class="hero-media-el" :src="media.poster" alt="" />
        <div class="hero-media-shade" aria-hidden="true"></div>
        <span class="hero-media-tag ld-mono">Placeholder / real recording goes here</span>
        <ul class="hero-readouts">
          <li v-for="r in readouts" :key="r.k" class="ld-readout">
            <span class="hero-readout-k">{{ r.k }}</span>
            <b>{{ r.v }}</b>
          </li>
        </ul>
      </div>

      <HeroScript />
    </div>
  </section>
</template>

<style scoped>
.hero {
  --hero-ink: var(--ld-navy-975);
  background-color: var(--hero-ink);
  padding-top: clamp(2.5rem, 6vw, 5rem);
  padding-bottom: 0;
}

.hero-top {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding-bottom: 1rem;
  border-bottom: 1px solid rgba(255, 255, 255, 0.12);
  margin-bottom: clamp(2rem, 5vw, 4rem);
}

.hero-h1 {
  font-size: clamp(2.75rem, 9.2vw, 8.25rem);
  line-height: 0.96;
  letter-spacing: -0.045em;
  max-width: 14ch;
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

.hero-btn-primary {
  background: #fff;
  color: var(--ld-navy-975) !important;
}
.hero-btn-primary img {
  filter: brightness(0);
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
    color-mix(in srgb, var(--ld-navy-975) 85%, transparent) 0%,
    color-mix(in srgb, var(--ld-navy-975) 15%, transparent) 45%,
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
  background: color-mix(in srgb, var(--ld-navy-975) 60%, transparent);
  color: rgba(255, 255, 255, 0.75);
  backdrop-filter: blur(6px);
}

.hero-readouts {
  position: absolute;
  left: 20px;
  right: 20px;
  bottom: 18px;
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem 2.5rem;
  list-style: none;
  margin: 0;
  padding: 0;
}

.hero-readouts .ld-readout {
  color: rgba(255, 255, 255, 0.6);
}
.hero-readout-k {
  text-transform: uppercase;
  letter-spacing: 0.08em;
  font-size: 0.7rem;
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

@media (max-width: 900px) {
  .hero-row {
    grid-template-columns: minmax(0, 1fr);
  }
  .hero-cta {
    justify-content: flex-start;
  }
}

@media (max-width: 640px) {
  .hero-top-right {
    display: none;
  }
  .hero-h1 {
    font-size: clamp(2.6rem, 12.5vw, 3.4rem);
  }
  .hero-media {
    aspect-ratio: 4 / 5;
  }
  .hero-cta .ld-btn {
    width: 100%;
  }
  .hero-readouts {
    flex-direction: column;
    gap: 0.55rem;
  }
  .hero-readouts .ld-readout {
    flex-direction: column;
    align-items: flex-start;
    gap: 0.15rem;
  }
  .hero-readout-k {
    white-space: nowrap;
  }
}
</style>
