---
layout: home
title: "Voice Keyboard for iPhone: Speech to Text"
description: Voice keyboard for iPhone with open-source server. On-device, self-hosted, or cloud transcription. No word limits, no tracking, 99 languages. Free forever on your own server.

hero:
  name: Diction
  text: "Speak to Type and Edit"
  tagline: "5x faster than typing, in any app.<br>On-device, self-hosted, or cloud. Your choice."
  actions:
    - theme: brand
      text: Download for iOS
      link: https://apps.apple.com/app/id6759807364
    - theme: alt
      text: GitHub
      link: https://github.com/omachala/diction

features:
  - icon:
      src: /mic-fill.svg
    title: Any App
    details: Fast and accurate speech to text iOS keyboard that works in any app. No word limits, no daily caps.
  - icon:
      src: /icon-infinity.svg
    title: No Limits
    details: No weekly caps, no word restrictions, no catch. Dictate as much as you want, whenever you want.
  - icon:
      src: /icon-phone.svg
    title: On-Device
    details: Speech recognition that runs locally on your iPhone. No internet required. Audio never leaves your device.
  - icon:
      src: /icon-github.svg
    title: Self-Hosted
    details: Run licensed or industry-specific models on your own server. Your network, your data.
    link: /features/self-hosting-setup
  - icon:
      src: /icon-shield.svg
    title: Private
    details: AES-256-GCM encryption with X25519 key exchange. The Diction app has zero analytics, zero tracking, zero data collection.
  - icon:
      src: /icon-globe.svg
    title: Your Language
    details: Automatic multilingual speech recognition. Speak in your language, or translate to another - Diction handles both.
  - icon:
      src: /icon-chat.svg
    title: Writing Tools
    details: Speak to edit, not just dictate. Say "translate this" or "make it more formal" and Diction rewrites your text on the spot. Per-app tones, custom dictionary, context-aware formatting.
  - icon:
      src: /icon-cloud.svg
    title: Diction One
    details: Cloud transcription with zero setup. Highest accuracy, lowest latency. Audio processed and immediately discarded.
---

<script setup>
import VPButton from 'vitepress/dist/client/theme-default/components/VPButton.vue'
import HowItWorks from './.vitepress/theme/HowItWorks.vue'
import TestimonialsSection from './.vitepress/theme/TestimonialsSection.vue'
import KeyboardThemes from './.vitepress/theme/KeyboardThemes.vue'
</script>

<HowItWorks />
<TestimonialsSection />
<KeyboardThemes />

<div class="cta-bottom">
<div class="VPHero"><div class="actions">
<VPButton tag="a" text="Download for iOS" href="https://apps.apple.com/app/id6759807364" theme="brand" />
</div></div>
</div>
