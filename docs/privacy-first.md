---
title: Privacy First
description: How Diction protects your voice data. On-device processing, self-hosted servers, encrypted transcriptions, open-source server code, and zero analytics in the app
keywords: voice keyboard privacy, ios keyboard full access, self hosted dictation, on-device speech to text, open source voice gateway, encrypted transcriptions
---

<img src="/illustration-privacy.svg" alt="Web security and data protection" class="illustration" style="max-width: 480px; margin: 0 auto 2rem; display: block;" />

# Privacy First

Voice keyboards are in a uniquely sensitive position. Every app you use, every message you type, every search you run, your keyboard is present for all of it. That kind of access demands more than a policy page.

Here is exactly how Diction handles your data.

## The problem with "trust us"

When you enable Full Access for a keyboard, you are extending significant trust. The permission exists so keyboards can do things like send audio for transcription or sync custom dictionaries. But in the wrong hands, it is also what would allow a keyboard to read what you type, monitor which apps you use, or send clipboard contents somewhere without disclosing it.

Closed-source keyboards can claim anything in a privacy policy. You have no way to verify what the code actually does.

Earlier this year, researchers examined a popular voice keyboard and found it was silently collecting full browser URLs, on-screen text via the Accessibility API, clipboard contents including data from password managers, and sending all of it to a server. Nothing in the privacy policy disclosed this. The only way it was discovered was by reverse-engineering the app.

This is why Diction exists the way it does.

## How Diction handles your audio

### <Icon name="phone" /> On-Device

Audio is processed entirely on your iPhone using local speech models. Nothing leaves your device. No internet connection is required. Audio is held in memory during transcription and discarded the moment the result comes back.

There is no server to breach. No policy to trust. No transmission to protect. The question of where your audio goes has one answer: nowhere.

### <Icon name="server" /> Self-Hosted

You point Diction at a server you control. Your audio travels to that server and nowhere else. No data touches Diction infrastructure. We have no access to your audio, your transcriptions, or your server.

The server software is open source. You can read it, audit it, and run it yourself.

### <Icon name="cloud" /> Diction One Cloud

Your audio is processed in memory and discarded the moment transcription completes. No recordings are written to disk. No transcriptions are stored, cached, or logged. Your audio is never used for model training.

Every transcription is protected with AES-256-GCM encryption and X25519 key exchange, the same standards used in WireGuard and Signal. Automatic on every request.

## <Icon name="lock" /> What the Diction app collects

Nothing.

The Diction app contains no analytics and no tracking code. No usage data, no device identifiers, no behavioural monitoring. Your App Store privacy label reads "Data Not Collected." That is accurate.

This website uses Google Analytics. The app does not.

Diction has no QWERTY keyboard. There is nothing to type into it, and therefore nothing to log.

## What you can verify

We do not ask you to take this on faith.

**Server code:** the gateway that handles your audio is [open source on GitHub](https://github.com/DictionLabs/Diction). Read the transcription handler. Verify that audio is not stored.

**Encryption:** the AES-256-GCM and X25519 implementation is in the same repository. Read it, audit it, or run it yourself.

**On-device mode:** no network requests leave the app. Confirm it with any network inspector.

---

On-device, self-hosted, or cloud. The principle is the same. Your voice is yours. We process it, return the text, and get out of the way.

[Download on the App Store](https://apps.apple.com/app/id6759807364) &nbsp;·&nbsp; [Server on GitHub](https://github.com/DictionLabs/Diction)
