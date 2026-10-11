<script setup lang="ts">
// Real screenshots of the keyboard, taken in the iOS simulator, light and dark
// side by side. Each one loops through the three heights you get by dragging
// the handle, so the picture itself says the keyboard resizes.
const heights = [
  { key: '', name: 'full', label: 'full keyboard' },
  { key: '-half', name: 'half', label: 'one row' },
  { key: '-bar', name: 'bar', label: 'just the bar' },
]
const themes = [
  { key: 'light', label: 'light mode' },
  { key: 'dark', label: 'dark mode' },
]
</script>

<template>
  <section class="ld-section kb-shots">
    <div class="ld-container">
      <div class="ld-head ld-center" v-reveal="{ delay: 60 }">
        <h2 class="ld-h2 big">One button does the job.</h2>
        <p class="ld-lead">
          Tap the Diction Bar and speak. Keep a full keyboard under it, one row of keys, or nothing but the bar.
        </p>
      </div>

      <div class="kb-pair" v-reveal="{ delay: 120 }">
        <figure v-for="t in themes" :key="t.key" class="kb-stage">
          <div class="kb-frame">
            <div class="kb-base">
            <img
              v-for="s in heights"
              :key="s.name"
              :class="`kb-${s.name}`"
              :src="`/keyboard-${t.key}${s.key}.webp`"
              :alt="`Diction keyboard, ${s.label}, ${t.label}`"
              loading="lazy"
              decoding="async"
            />
            </div>
            <!-- The grabber and Diction Bar, cut from the full shot, ride the top
                 edge down like a drawer closing over the key rows. -->
            <div class="kb-head" aria-hidden="true">
              <img :src="`/keyboard-${t.key}.webp`" alt="" loading="lazy" decoding="async" />
            </div>
          </div>
        </figure>
      </div>
    </div>
  </section>
</template>

<style scoped>
.kb-shots .ld-lead {
  max-width: 560px;
  margin-inline: auto;
}

.kb-pair {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: clamp(1rem, 4vw, 3.5rem);
  max-width: 980px;
  margin-inline: auto;
}

/* Heights are in cqw of the stage width, from the screenshots:
   full 1206x1029, one row 1206x564, bar 1206x419. */
.kb-stage {
  --kb-full: 85.32cqw;
  --kb-half: 46.77cqw;
  --kb-bar: 34.74cqw;
  container-type: inline-size;
  position: relative;
  margin: 0;
}
/* Reserve the full height so the page never jumps while the keyboard shrinks. */
.kb-stage::before {
  content: '';
  display: block;
  height: var(--kb-full);
}
/* The keyboard stays pinned to the bottom, as on the phone, and loses height
   from the top. */
.kb-frame {
  position: absolute;
  inset: auto 0 0 0;
  height: var(--kb-full);
  overflow: hidden;
  /* the keyboard's top corner radius, so nothing underneath shows past it */
  animation: kb-height 10s cubic-bezier(0.65, 0, 0.35, 1) infinite;
}
/* The rows live below the head, so nothing of them shows around its corners. */
.kb-base {
  position: absolute;
  inset: 15.9cqw 0 0 0;
  overflow: hidden;
}
.kb-frame img {
  position: absolute;
  left: 0;
  bottom: 0;
  width: 100%;
  height: auto;
}
/* The full keyboard is the backdrop: the bar slides over its rows. The
   shorter shots sit on top without their own bar, so only their key row fades. */
.kb-half,
.kb-bar {
  clip-path: inset(15.9cqw 0 0 0);
}
.kb-half {
  opacity: 0;
  animation: kb-half 10s cubic-bezier(0.65, 0, 0.35, 1) infinite;
}
.kb-bar {
  opacity: 0;
  animation: kb-bar 10s cubic-bezier(0.65, 0, 0.35, 1) infinite;
}

/* Full, down to one row, down to the bar, back up through one row to full. */
@keyframes kb-height {
  0%, 20% { height: var(--kb-full); }
  26%, 42% { height: var(--kb-half); }
  48%, 64% { height: var(--kb-bar); }
  70%, 80% { height: var(--kb-half); }
  86%, 100% { height: var(--kb-full); }
}
/* The rows underneath blend across each move, never in a single frame, so
   nothing flicks before the keyboard starts moving. Height moves are at
   20-26, 42-48, 64-70 and 80-86 percent. */
@keyframes kb-half {
  0%, 20% { opacity: 0; }
  26%, 42% { opacity: 1; }
  48%, 64% { opacity: 0; }
  70%, 80% { opacity: 1; }
  86%, 100% { opacity: 0; }
}
@keyframes kb-bar {
  0%, 42% { opacity: 0; }
  48%, 64% { opacity: 1; }
  70%, 100% { opacity: 0; }
}

/* 190 of 1206 px: grabber and Diction Bar, the top of every height. */
.kb-head {
  position: absolute;
  inset: 0 0 auto 0;
  height: 15.9cqw;
  overflow: hidden;
}
.kb-head img {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: auto;
  animation: none;
  opacity: 1;
}

@media (prefers-reduced-motion: reduce) {
  .kb-frame,
  .kb-frame img {
    animation: none;
  }
  .kb-head {
    display: none;
  }
  .kb-half,
  .kb-bar {
    display: none;
  }
}

/* Phones: one above the other, still animating. */
@media (max-width: 640px) {
  .kb-pair {
    grid-template-columns: minmax(0, 1fr);
    max-width: 420px;
  }
}
</style>
