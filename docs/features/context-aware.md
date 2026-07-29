---
title: "Context-Aware Text Editing"
description: Diction reads the text around your cursor and adapts your dictation to fit. Insert into sentences, replace selected text, fix typos -- all by voice.
---

<img src="/illustration-context-aware.svg" alt="Email and text editing" class="illustration" style="max-width: 480px; margin: 0 auto 2rem; display: block;" />

# Context-Aware Text Editing

Diction is not a blank-page dictation tool. It sees where your cursor is, reads the text before and after the insertion point, and understands what you have selected. When Writing Tools are active, every dictation is shaped to fit the context it is landing in.

This turns Diction from a transcription tool into a voice-powered text editor.

## You don't pick a mode

Diction figures out what you want from where your cursor is.

- **Cursor in empty space**: it dictates. Words land at the cursor.
- **Cursor in the middle of text**: it inserts. The new words fit the surrounding sentence.
- **Text selected**: it edits. Either replaces the selection with what you said, or follows your instruction. Say *"translate to Czech"* and it does that.

No toggle, no setting. One mic button, the right behaviour for the context you are in.

## Insert Anywhere

Place your cursor in the middle of a sentence and dictate. Diction reads the surrounding text and formats the insertion to fit. Capitalisation and spacing adapt automatically, so the result reads like you typed it in place.

**Example: filling in a sentence**

You are writing an email:

> Hi Sarah, I wanted to follow up on ▌ and see if you had any questions.

You tap the mic and say: *"the proposal I sent last week"*

Result:

> Hi Sarah, I wanted to follow up on **the proposal I sent last week** and see if you had any questions.

No stray capital letter. No extra period. It fits because Diction sees the sentence around it.

---

**Example: continuing a list**

You have a note open:

> Things to pack:
> - Passport
> - Charger
> ▌

You tap the mic and say: *"laptop and headphones"*

Result:

> Things to pack:
> - Passport
> - Charger
> - **Laptop and headphones**

It matches the list format because it can see the pattern above.

---

**Example: adding to a code comment**

You are editing a code file:

> // TODO: refactor this function to ▌

You say: *"handle the edge case where the input array is empty"*

Result:

> // TODO: refactor this function to **handle the edge case where the input array is empty**

Lowercase, no period. It reads like you typed it.

## Select and Replace

Select text in any app, then dictate. Diction replaces the selection with your new words. This is the fastest way to rewrite, rephrase, or fix text by voice.

**Example: rewriting a sentence**

Your draft says:

> I think we should probably consider maybe going with the second option if that is okay with everyone.

Select the whole sentence. Tap the mic. Say: *"Let's go with option B."*

Result:

> **Let's go with option B.**

No copy-paste. No deleting. Select, speak, replaced.

---

**Example: fixing a single word**

You wrote "defiantly" when you meant "definitely". Double-tap the word to select it. Tap the mic. Say *"definitely"*. Replaced.

---

**Example: rephrasing a paragraph**

You have a paragraph in a document that does not sound right. Select it. Dictate what you actually want to say. The entire selection is replaced with your new dictation, formatted to fit the surrounding document.

## How It Works

Every time you tap the mic, Diction captures:

- The text before your cursor (up to 500 characters)
- The text after your cursor (up to 500 characters)
- Any selected text

This context is sent alongside your audio. Writing Tools use it to decide capitalisation, punctuation, formatting, and tone. The result is text that reads like it was typed in place, not pasted from a separate dictation box.

## When Is Context Used?

Context-aware editing requires **Writing Tools** to be active. Writing Tools are available with Diction One cloud.

Without Writing Tools (on-device or self-hosted without cloud post-processing), Diction still handles select-and-replace correctly. Your dictation replaces the selection. But the formatting will not adapt to surrounding text.

| Mode | Select and Replace | Context-Aware Formatting |
|------|-------------------|------------------------|
| On-Device | Yes | -- |
| Self-Hosted | Yes | -- |
| Diction One Cloud | Yes | Yes |
| Diction One Cloud (Writing Tools off) | Yes | . |

## Tips

- **Place your cursor precisely.** The more context Diction has on both sides, the better the formatting. Tap to position your cursor where you want the text to go.
- **Select generously.** When replacing, select the full phrase or sentence you want to rewrite. Diction works best when it can see the whole thing you are replacing.
- **Speak naturally.** You do not need to dictate punctuation or capitalisation. Writing Tools handle it based on context.
- **Use it for edits, not just dictation.** Diction is most powerful when you are refining existing text. First drafts, rewrites, quick fixes -- it handles all of them.
