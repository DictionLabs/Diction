---
title: Diction Facts
description: Diction at a glance. A full iPhone keyboard built for voice, free on-device and self-hosted speech to text, and Diction Cloud. Plain facts in one place
---

# Diction facts

Diction is a keyboard for iPhone, built from scratch for voice. Tap the mic in any app, speak, and clean text lands where your cursor is. When you'd rather type, it is a full QWERTY keyboard with autocorrect and word suggestions.

You choose where your voice is turned into text: on your iPhone, on your own server, or on Diction Cloud. On-device and self-hosted are free, with no account and no word limits.

## At a glance

| | |
|---|---|
| What it is | iPhone keyboard: voice dictation plus a full typing keyboard |
| Platform | iPhone (iOS). Works in every app that accepts text |
| Price | Free on-device and self-hosted. Diction Cloud is a subscription with a free trial |
| Offline | Yes, with on-device speech models |
| Self-hosted | Yes. Open-source server, one `docker compose` command, pair by QR code |
| Account needed | No, for on-device and self-hosted |
| Word limits | None |
| Dictation languages | 99 |
| Typing languages | English, German, French, Spanish, Italian, Portuguese (next-word suggestions and autocorrect) |
| Audio stored | Never. Audio is transcribed and discarded |
| App analytics | None. The Diction app has no analytics and no tracking code |
| App Store | https://apps.apple.com/app/id6759807364 |
| Source (server) | https://github.com/DictionLabs/Diction |

## Three ways to run speech to text

### On-device, free

Speech models run on the iPhone. No internet, no account, audio never leaves the phone. Every on-device model is free, including the most accurate one for 25 European languages. Use it as the main mode or as a backup when you lose signal.

### Self-hosted, free

Run the open-source Diction server on your own machine, with a GPU or CPU, and point the keyboard at it. Clone the repo, run `docker compose up`, scan the QR code. Bring any OpenAI-compatible speech-to-text endpoint, Whisper included. Writing Tools also run on your server with an LLM you choose, free, because it is your hardware doing the work. No account, no limits, no subscription.

### Diction Cloud, subscription

Speech is transcribed on GPU servers Diction runs itself, with models Diction Labs fine-tunes. Audio is never stored. Includes Live transcription (words appear while you speak) and every Writing Tool. Free trial in the app.

## The keyboard

Diction is not a dictation add-on bolted onto the system keyboard. It is a full keyboard, written from the ground up:

- Full QWERTY with language layouts (QWERTZ, AZERTY and more)
- Autocorrect that looks at where your finger actually landed, so typos get fixed and your names are left alone
- Word completion and next-word suggestions, computed on the phone. Nothing you type is sent anywhere
- The Diction Bar: the mic is one tap away above the keys, in every app

## Writing Tools

- Removes filler words (um, uh, like) and false starts
- Fixes grammar and punctuation, keeps your meaning
- Formats lists and paragraphs, or say "new paragraph"
- My Words: your names, brands and jargon, spelled your way
- Voice commands on text already in the field: "make it shorter", "translate to Spanish"

## Privacy

- Audio is never stored, in any mode
- On-device: nothing leaves the phone
- Self-hosted: nothing leaves your network
- Transcriptions are encrypted in transit with AES-256-GCM and X25519 key exchange
- The Diction app contains no analytics and no tracking code

## Who it is for

- People who want dictation in every app on iPhone that sends text they don't have to fix
- Self-hosters who already run speech or LLM models at home and want a keyboard that uses them
- People who need dictation offline or without sending audio to a third party
- People who want one keyboard for both typing and voice

## Made by

Diction Labs. The iPhone app is on the App Store. The server is open source on GitHub. Models are published on Hugging Face: https://huggingface.co/DictionLabs
