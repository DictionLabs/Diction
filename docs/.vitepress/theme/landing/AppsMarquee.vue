<script setup lang="ts">
type Item = { icon: string } | { chip: string }

const baseA: Item[] = [
  { icon: 'whatsapp' },
  { icon: 'messages' },
  { icon: 'mail' },
  { chip: 'Any text field' },
  { icon: 'notion' },
  { icon: 'slack' },
  { icon: 'notes' },
  { chip: 'No plugin needed' },
  { icon: 'gmail' },
  { icon: 'safari' },
]

const baseB: Item[] = [
  { icon: 'instagram' },
  { icon: 'messenger' },
  { icon: 'discord' },
  { chip: 'Any app' },
  { icon: 'telegram' },
  { icon: 'x' },
  { icon: 'whatsapp' },
  { icon: 'notes' },
  { icon: 'gmail' },
]

// Each row is the base list repeated REPEAT times so a single copy is wider
// than any viewport we care about (>= 2x 1440px). The track then holds two
// identical copies and animates by exactly -50%, so the loop seam lands on
// the same pixels. Items use margin-right instead of flex gap so both copies
// are exactly equal in width (a trailing gap would break the seam).
const REPEAT = 4
function repeat(list: Item[]): Item[] {
  const out: Item[] = []
  for (let i = 0; i < REPEAT; i++) out.push(...list)
  return out
}
const rowA = repeat(baseA)
const rowB = repeat(baseB)

function isIcon(item: Item): item is { icon: string } {
  return 'icon' in item
}
</script>

<template>
  <section class="ld-section apps-marquee">
    <div class="ld-container" v-reveal>
      <div class="ld-label-row marquee-head">
        <span class="ld-mono ld-accent ld-blue">01 / Works everywhere you type</span>
        <span class="ld-mono">Any app, any text field</span>
      </div>
    </div>

    <div class="marquee-row" v-reveal="{ delay: 80 }">
      <div class="marquee-track">
        <div v-for="copy in 2" :key="copy" class="marquee-copy" :aria-hidden="copy === 2 ? 'true' : undefined">
          <template v-for="(item, i) in rowA" :key="`a-${copy}-${i}`">
            <img
              v-if="isIcon(item)"
              class="marquee-icon"
              :src="`/app-icons/${item.icon}.png`"
              :alt="copy === 1 ? item.icon : ''"
              width="56"
              height="56"
              loading="lazy"
            />
            <span v-else class="marquee-chip">{{ item.chip }}</span>
          </template>
        </div>
      </div>
    </div>

    <div class="marquee-row reverse" v-reveal="{ delay: 140 }">
      <div class="marquee-track">
        <div v-for="copy in 2" :key="copy" class="marquee-copy" :aria-hidden="copy === 2 ? 'true' : undefined">
          <template v-for="(item, i) in rowB" :key="`b-${copy}-${i}`">
            <img
              v-if="isIcon(item)"
              class="marquee-icon"
              :src="`/app-icons/${item.icon}.png`"
              :alt="copy === 1 ? item.icon : ''"
              width="56"
              height="56"
              loading="lazy"
            />
            <span v-else class="marquee-chip">{{ item.chip }}</span>
          </template>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.apps-marquee {
  padding-block: 3rem;
}
.apps-marquee .ld-container {
  margin-bottom: 2rem;
}
.apps-marquee .marquee-head {
  margin-bottom: 1.75rem;
}

.marquee-row {
  overflow: hidden;
  width: 100%;
  -webkit-mask-image: linear-gradient(to right, transparent, black 10%, black 90%, transparent);
  mask-image: linear-gradient(to right, transparent, black 10%, black 90%, transparent);
}
.marquee-row + .marquee-row {
  margin-top: 1.25rem;
}

.marquee-track {
  display: flex;
  align-items: center;
  width: max-content;
  will-change: transform;
  animation: marquee-left 150s linear infinite;
}
.marquee-copy {
  display: flex;
  align-items: center;
  flex: 0 0 auto;
}
.marquee-row:hover .marquee-track {
  animation-play-state: paused;
}
.marquee-row.reverse .marquee-track {
  animation-name: marquee-right;
}

@keyframes marquee-left {
  from {
    transform: translateX(0);
  }
  to {
    transform: translateX(-50%);
  }
}
@keyframes marquee-right {
  from {
    transform: translateX(-50%);
  }
  to {
    transform: translateX(0);
  }
}

.marquee-icon,
.marquee-chip {
  margin-right: 1.25rem;
  flex: 0 0 auto;
}
.marquee-icon {
  width: 56px;
  height: 56px;
  border-radius: 22%;
  box-shadow: var(--ld-shadow-sm);
  background: var(--ld-surface);
}
.marquee-chip {
  display: inline-flex;
  align-items: center;
  height: 56px;
  padding: 0 20px;
  border-radius: var(--ld-r-pill);
  border: 1px solid var(--vp-c-divider);
  background: transparent;
  color: var(--vp-c-text-3);
  font-size: 0.875rem;
  font-weight: 500;
  white-space: nowrap;
}

@media (prefers-reduced-motion: reduce) {
  .marquee-track {
    animation: none;
  }
}

@media (max-width: 640px) {
  .apps-marquee {
    padding-block: 2.5rem;
  }
  .marquee-icon,
  .marquee-chip {
    margin-right: 1rem;
  }
  .marquee-icon {
    width: 48px;
    height: 48px;
  }
  .marquee-chip {
    height: 48px;
    padding: 0 16px;
  }
}
</style>
