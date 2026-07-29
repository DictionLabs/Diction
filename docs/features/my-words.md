---
title: "My Words"
description: Add names, places, and terms to your personal dictionary. Diction uses them to improve transcription and Writing Tools accuracy
keywords: "custom dictionary voice keyboard, speech recognition custom words, add words dictation, improve transcription accuracy, voice keyboard custom vocabulary, custom vocabulary ios"
---

<img src="/illustration-my-words.svg" alt="Custom dictionary" class="illustration" style="max-width: 480px; margin: 0 auto 2rem; display: block;" />

# My Words

Speech models handle everyday words fine. But your friend's last name, the street you live on, the brand you mention ten times a day? Those get garbled.

You say "Nguyen" and get "new win." You say "Venmo" and it comes out as "ven mo." Your dog's name Biscotti becomes "this coffee."

My Words fixes this. Add your words once, and Diction gets them right every time.

## How It Works

Open the Diction app and go to My Words. Add any word or phrase that keeps getting misheard. Each entry can include "also heard as" variants for how the word might come out wrong.

For example:

| Word | Also heard as |
|------|--------------|
| Nguyen | New Win, Winn |
| Venmo | Ven Mo, Ben Mo |
| Acai | Ah Sigh, A Kai |
| Peloton | Pellet On, Pell A Ton |

When you dictate, Diction checks your custom words against the transcription. The speech model uses your words as hints during recognition, and Writing Tools correct any remaining mismatches. Both layers work together.

## What It Catches

My Words works at two levels:

1. **During transcription.** Your custom words nudge the speech model toward the right word when the audio is ambiguous. "Nguyen" is more likely to come through correctly because the model knows to listen for it.

2. **During cleanup.** Even if the raw transcription still gets it wrong, Writing Tools know your words and swap in the correct spelling. "New Win" becomes "Nguyen" because Diction knows that is what you meant.

The combination catches most cases. The speech model gets it right more often, and the cleanup catches what slips through.

## Good Candidates for My Words

- People's names (friends, family, coworkers)
- Brand and company names
- Street names and neighborhoods
- Restaurant names, food terms
- Anything you say regularly that keeps coming out wrong

## Requirements

My Words lives on the [Writing Tools](/features/writing-tools) screen in the app. The cleanup layer requires a Diction One subscription, and works across all transcription modes.

Your custom words improve both raw transcription (via hints to the speech model) and Writing Tools (via the cleanup step). The hints work whether or not Writing Tools are on, but the cleanup correction layer needs them.
