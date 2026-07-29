---
title: Support
description: Get help with Diction voice keyboard for iPhone. Setup guide, troubleshooting, and contact information.
---

<img src="/illustration-support.svg" alt="Video call support" class="illustration" style="max-width: 480px; margin: 0 auto 2rem; display: block;" />

# Support

## Getting Started

Setting up Diction takes under a minute:

1. **Open the Diction app** and grant microphone permission when prompted.
2. Go to **Settings > General > Keyboard > Keyboards > Add New Keyboard** and select **Diction**.
3. Tap **Diction** in the keyboard list and enable **Allow Full Access** (required for transcription).
4. Open any app with a text field, tap the **globe icon** to switch to Diction, and tap the mic.

::: tip
The Diction app must be running in the background for the keyboard to work. Launch it once and it stays ready. After a period of inactivity, you may need to open the app again.
:::

## Troubleshooting

### Keyboard does not appear

Make sure Diction is added in **Settings > General > Keyboard > Keyboards**. If it still does not appear, restart your iPhone.

### Microphone not working

Open the Diction app and grant microphone permission. The keyboard extension cannot request mic access on its own. Permission must be granted through the main app first.

### "Open Diction to start" message

The Diction app needs to be running in the background. Open the app, then switch back to your text field and try again.

### "Enable Full Access" message

Go to **Settings > General > Keyboard > Keyboards > Diction** and enable **Allow Full Access**. iOS requires this for any keyboard that uses network access.

### On-device transcription not working

Make sure you have downloaded a speech model in the Diction app. The Standard model downloads automatically on first launch. Check the On-Device section in the app to confirm.

### Transcription fails or times out

Check your internet connection (not needed for on-device mode). If you are using a self-hosted server, verify your endpoint URL is correct and the server is reachable from your phone's network.

## Self-Hosting

Diction works with any speech-to-text server that supports the standard transcription API format. For setup guides, Docker Compose files, and documentation, see the [GitHub repository](https://github.com/omachala/diction).

## Managing Your Subscription

To manage or cancel your Diction One subscription:

**Settings > Apple ID > Subscriptions** on your iPhone.

You can cancel at any time. Your subscription remains active until the end of the current billing period.

<div class="faq-section">

## Questions

<details>
<summary>Is it really free?</summary>
<div>

On-device and self-hosted modes are completely free. No word limits, no daily caps. Diction One unlocks cloud transcription with the highest accuracy, plus Writing Tools with per-app tones and a custom dictionary. Free trial included.

</div>
</details>

<details>
<summary>How is it better than Apple Dictation?</summary>
<div>

More accurate speech models, no session time limits, no word caps. Writing Tools clean up filler words and grammar automatically. Context-aware editing reads the text around your cursor so dictating mid-sentence produces correct capitalization and punctuation. Set a tone per app, add your own words for names and jargon, and search your full dictation history. Apple offers none of that.

</div>
</details>

<details>
<summary>Can I edit existing text by voice?</summary>
<div>

Yes. Diction reads the text around your cursor. Dictate into the middle of a sentence and it inserts with correct capitalization and punctuation. Select text and speak to replace it. Rewrite a sentence, fix a typo, or add to a paragraph, all without touching the screen.

</div>
</details>

<details>
<summary>Does it work offline?</summary>
<div>

Yes. On-device mode works without internet once the model is downloaded. Cloud and self-hosted modes require network access.

</div>
</details>

<details>
<summary>What languages does Diction support?</summary>
<div>

99 languages. On-device handles most languages well. Cloud and self-hosted use larger models for even better accuracy across all supported languages.

</div>
</details>

<details>
<summary>Is my voice data stored?</summary>
<div>

Never. On-device mode processes audio in memory and discards it immediately. Self-hosted mode sends audio only to your server. Diction One cloud processes and discards. No recordings retained, no model training.

</div>
</details>

<details>
<summary>What are Writing Tools?</summary>
<div>

After transcription, Diction can optionally clean up your text. It removes filler words, fixes grammar, and polishes the result. Set a tone per app (Professional for email, Casual for chat) and add your own words to a custom dictionary so names and jargon come through right. Only the text is sent to the AI, never the audio.

</div>
</details>

<details>
<summary>How do I set it up?</summary>
<div>

Open the app, grant microphone permission, add Diction as a keyboard in iOS Settings, enable Full Access, and start dictating. Under a minute from download to first transcription. [Detailed steps here.](/support)

</div>
</details>

<details>
<summary>What is self-hosting?</summary>
<div>

You run a speech-to-text server on your own hardware. Diction connects to it over your network. Your audio never touches any third-party service. The server ships as a Docker image. One command to start.

</div>
</details>

<details>
<summary>Why does it need Full Access?</summary>
<div>

iOS requires Full Access for any keyboard extension that uses the network. Diction needs it to send audio to your server or Diction One for transcription. Diction has no QWERTY keys to log, does not read your clipboard, and does not access contacts or any other personal data.

</div>
</details>

</div>

## Contact

Need help? Reach out:

- **Email:** [support@diction.one](mailto:support@diction.one)
- **GitHub:** [Open an issue](https://github.com/omachala/diction/issues)
- **Reddit:** [r/dictionapp](https://www.reddit.com/r/dictionapp)
- **X:** [@diction_one](https://x.com/diction_one)
