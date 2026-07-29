<script setup>
// Icon/bubble positions lifted from appstore/slides heroLayout.ts (left-half subset,
// x normalized 0-1 as fraction of container width, y 0-1 as fraction of container height).
const ICONS = [
  { src: '/app-icons/whatsapp.png', x: 0.08, y: 0.06, size: 0.09, opacity: 1, rotation: -6 },
  { src: '/app-icons/safari.png', x: 0.86, y: 0.02, size: 0.08, opacity: 0.9, rotation: 5 },
  { src: '/app-icons/instagram.png', x: 0.00, y: 0.46, size: 0.085, opacity: 0.9, rotation: -3 },
  { src: '/app-icons/messenger.png', x: 0.90, y: 0.44, size: 0.08, opacity: 0.85, rotation: 4 },
  { src: '/app-icons/mail.png', x: 0.08, y: 0.84, size: 0.085, opacity: 0.85, rotation: -4 },
  { src: '/app-icons/messages.png', x: 0.72, y: 0.86, size: 0.08, opacity: 0.8, rotation: 6 },
]

const BUBBLES = [
  { text: 'Translate', x: 0.26, y: 0.00, opacity: 1 },
  { text: 'Transcribe', x: 0.92, y: 0.26, opacity: 1 },
  { text: 'Edit', x: 0.78, y: 0.65, opacity: 0.9 },
  { text: 'Fix grammar', x: 0.00, y: 0.26, opacity: 0.9 },
  { text: 'Rewrite', x: -0.14, y: 0.65, opacity: 0.85 },
  { text: 'Compose', x: 0.32, y: 1.02, opacity: 0.85 },
]
</script>

<template>
  <div class="hero-decorations">
    <img
      v-for="(icon, i) in ICONS"
      :key="`icon-${i}`"
      :src="icon.src"
      alt=""
      draggable="false"
      class="hero-decoration-icon"
      :style="{
        left: `${icon.x * 100}%`,
        top: `${icon.y * 100}%`,
        width: `${icon.size * 100}%`,
        opacity: icon.opacity,
        transform: `rotate(${icon.rotation}deg)`,
      }"
    />
    <div
      v-for="(bubble, i) in BUBBLES"
      :key="`bubble-${i}`"
      class="hero-decoration-bubble"
      :style="{ left: `${bubble.x * 100}%`, top: `${bubble.y * 100}%`, opacity: bubble.opacity }"
    >
      <svg viewBox="0 0 24 24" fill="currentColor" class="hero-decoration-bubble-icon">
        <path d="M8.25 4.5C8.25 2.43 9.93.75 12 .75s3.75 1.68 3.75 3.75v8.25c0 2.07-1.68 3.75-3.75 3.75s-3.75-1.68-3.75-3.75V4.5z" />
        <path d="M6 10.5a.75.75 0 0 1 .75.75v1.5a5.25 5.25 0 0 0 10.5 0v-1.5a.75.75 0 0 1 1.5 0v1.5a6.75 6.75 0 0 1-6 6.71v2.29h3a.75.75 0 0 1 0 1.5h-7.5a.75.75 0 0 1 0-1.5h3v-2.29a6.75 6.75 0 0 1-6-6.71v-1.5a.75.75 0 0 1 .75-.75z" />
      </svg>
      {{ bubble.text }}
    </div>
  </div>
</template>

<style scoped>
.hero-decorations {
  position: absolute;
  inset: 0;
  pointer-events: none;
}

.hero-decoration-icon {
  position: absolute;
  aspect-ratio: 1;
  border-radius: 22%;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.35);
}

.hero-decoration-bubble {
  position: absolute;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  border-radius: 16px 16px 16px 3px;
  border: 1px solid var(--diction-blue);
  color: var(--diction-blue);
  font-size: 0.85rem;
  font-weight: 500;
  white-space: nowrap;
}

.dark .hero-decoration-bubble {
  border-color: var(--diction-blue-light);
  color: var(--diction-blue-light);
}

.hero-decoration-bubble-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}

@media (max-width: 1279px) {
  .hero-decoration-bubble {
    font-size: 0.75rem;
    padding: 6px 11px;
  }
}

@media (max-width: 959px) {
  .hero-decorations {
    display: none;
  }
}
</style>
