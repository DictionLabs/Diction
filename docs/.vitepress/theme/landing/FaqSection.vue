<script setup lang="ts">
// Written for the two people who land here: a self-hoster and someone leaving
// another dictation app. Every answer must stay true for the App Store build.
const faqs = [
  {
    q: 'Is Diction free?',
    a: 'On your iPhone and on your server, yes. Free forever, no word limits, no account. Diction Cloud is the paid option at $5.99 a month or $59.99 a year, and you can try it free first.',
  },
  {
    q: 'Does it work in every app?',
    a: 'Pretty much anywhere you can type: Messages, Mail, Slack, Notes, your browser. Two exceptions come from iOS itself. Password fields always get the system keyboard, and a few apps block custom keyboards.',
  },
  {
    q: 'Does it work offline?',
    a: 'Yes, in on-device mode. Download a speech AI once and dictate on a plane, in a tunnel, anywhere. Diction Cloud and self-hosted need a connection, because that is where the speech AI runs.',
  },
  {
    q: 'Is my voice stored?',
    a: 'No. On-device, your voice never leaves the phone. Self-hosted, it goes to your server and nowhere else. On Diction Cloud it is transcribed and thrown away.',
  },
  {
    q: 'Why does the keyboard need Full Access?',
    a: 'iOS asks for it before any keyboard can use the network, and Diction needs the network to reach the speech AI. That is all it is used for.',
  },
  {
    q: 'Which languages does it speak?',
    a: '99 for dictation, with auto-detect if you switch between them. 25 European languages get the most accurate engine.',
  },
  {
    q: 'Will it get my name right?',
    a: 'Add it to My Words once, along with your colleagues, brands and jargon, and Diction spells them your way from then on.',
  },
  {
    q: 'Can I edit text by voice?',
    a: 'Yes. Hold the mic and say what you want: "make it a list", "translate it to Spanish", "remove the last sentence". The text that is already there changes.',
  },
  {
    q: 'What is self-hosting?',
    a: 'You run the Diction server on a machine you control, and the app sends your voice there instead of the cloud. It is open source, it runs in Docker, and pairing is one QR scan.',
  },
  {
    q: 'Do I need a GPU to self-host?',
    a: 'It helps. An NVIDIA GPU gives the fastest results. No GPU? The smaller Whisper models run fine on a CPU.',
  },
  {
    q: 'Can Writing Tools use an LLM I run at home?',
    a: 'Yes. Point the server at any OpenAI-compatible endpoint, like Ollama, and Writing Tools run on it, free.',
  },
  {
    q: 'Is there an Android or Mac app?',
    a: 'Not yet. Diction is iPhone first, and that is where the work goes right now.',
  },
  {
    q: 'How do I cancel?',
    a: 'The same way as any App Store subscription, in your Apple ID settings. On-device and self-hosted keep working, free, after you cancel.',
  },
]
</script>

<template>
  <section class="ld-section faq-landing">
    <div class="ld-container narrow">
      <div class="ld-head ld-center" v-reveal>
        <h2 class="ld-h2 big">Good questions.</h2>
      </div>

      <div class="faq-list" v-reveal="{ delay: 80 }">
        <details v-for="(faq, i) in faqs" :key="faq.q" class="faq-item" :open="i === 0">
          <summary class="faq-summary">
            <span class="faq-q">{{ faq.q }}</span>
            <span class="faq-icon" aria-hidden="true">
              <span class="bar bar-h"></span>
              <span class="bar bar-v"></span>
            </span>
          </summary>
          <div class="faq-body">
            <p class="faq-body-inner">{{ faq.a }}</p>
          </div>
        </details>
      </div>
    </div>
  </section>
</template>

<style scoped>
.faq-list {
  border-top: 1px solid var(--vp-c-divider);
}

.faq-item {
  border-bottom: 1px solid var(--vp-c-divider);
}

.faq-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1.5rem;
  padding: 1.6rem 0;
  cursor: pointer;
  list-style: none;
  font-family: 'Libre Franklin', var(--vp-font-family-base);
  font-size: clamp(1.2rem, 1.9vw, 1.5rem);
  font-weight: 650;
  letter-spacing: -0.015em;
  line-height: 1.25;
  color: var(--vp-c-text-1);
  transition: color 0.2s ease;
}
.faq-summary:hover,
.faq-item[open] .faq-summary {
  color: var(--ld-violet-blue);
}
.faq-summary::-webkit-details-marker {
  display: none;
}
.faq-summary::marker {
  content: '';
}

/* Plus in a circle; turns into a minus when open. */
.faq-icon {
  position: relative;
  width: 36px;
  height: 36px;
  flex-shrink: 0;
  border-radius: 50%;
  border: 1px solid var(--vp-c-divider);
  transition: border-color 0.2s ease, background-color 0.2s ease;
}
.faq-item[open] .faq-icon {
  border-color: transparent;
  background: color-mix(in srgb, var(--ld-violet-blue) 12%, transparent);
}
.faq-icon .bar {
  position: absolute;
  inset: 0;
  margin: auto;
  width: 14px;
  height: 2px;
  border-radius: 2px;
  background: currentColor;
  transition: transform 0.25s var(--ld-ease);
}
.faq-icon .bar-v {
  transform: rotate(90deg);
}
.faq-item[open] .faq-icon .bar-v {
  transform: rotate(0deg);
}

.faq-body {
  display: grid;
  grid-template-rows: 0fr;
  transition: grid-template-rows 0.3s var(--ld-ease);
}
.faq-item[open] .faq-body {
  grid-template-rows: 1fr;
}
.faq-body-inner {
  overflow: hidden;
  min-height: 0;
}
.faq-body-inner p,
.faq-body p {
  margin: 0 0 1.75rem;
  padding-right: 3.5rem;
  max-width: 44rem;
  color: var(--vp-c-text-2);
  font-size: clamp(1.05rem, 1.4vw, 1.1875rem);
  line-height: 1.6;
}

/* Force the content wrapper to stay laid out (grid) instead of the
   browser's default display:none when the <details> is closed, so the
   grid-template-rows transition can actually animate. */
.faq-item:not([open]) .faq-body,
.faq-item[open] .faq-body {
  display: grid;
}

@media (max-width: 640px) {
  .faq-summary {
    padding: 1.25rem 0;
  }
  .faq-icon {
    width: 30px;
    height: 30px;
  }
}
</style>
