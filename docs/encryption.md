---
title: Encrypted Transcriptions
description: Diction protects every transcription with AES-256-GCM encryption and X25519 key exchange. The same standards used in WireGuard and Signal. Automatic. No configuration required.
---

<img src="/illustration-encryption.svg" alt="File security and encryption" class="illustration" style="max-width: 480px; margin: 0 auto 2rem; display: block;" />

# Your Words. Protected.

Privacy is built into Diction at the protocol level. Not as a feature you enable. Not as a paid tier. As a default.

Every transcription is protected with AES-256-GCM encryption and X25519 key exchange. These are the same cryptographic standards used in WireGuard, Signal, and TLS 1.3. We did not invent anything. We applied the best tools the industry has, correctly, and made them automatic.

## We Take This Seriously

Diction was built for people who think about where their voice goes. The server infrastructure is fully open source. The encryption is standard, auditable, and ships in every build. There is no version of Diction that does not encrypt your transcriptions.

We do not ask you to trust us. We give you the code.

## What This Means For You

- **Encrypted on every request**: your transcriptions are protected with AES-256-GCM before they leave the server. Strong encryption, every time, without exception.
- **Fresh key per request**: X25519 key exchange generates a unique session key for each transcription. No key is reused. No key is stored. Nothing accumulates that could be stolen.
- **Automatic on Diction One and self-hosted**: the same protection runs on our cloud and on every community-deployed gateway. No configuration. No opt-in.
- **Open-source implementation**: the encryption code is public on GitHub. Read it, audit it, run it yourself.

## Writing Tools: Encrypted Both Ways

When Writing Tools clean up your transcription, the text makes a round trip. Your raw words go to the server. The polished version comes back.

Both directions are encrypted.

Your text is sealed on your device before it leaves. The server decrypts it, runs the cleanup, and encrypts the result with a fresh key before sending it back. At no point does your text travel unprotected.

This is not something you configure. It happens on every Writing Tools request, automatically.

## On-Device Is Still the Gold Standard

If absolute privacy is your requirement, on-device transcription is the answer. Audio never leaves your iPhone. There is nothing to encrypt because there is no transmission. Encryption protects data in transit. On-device removes the transit entirely.

For everyone else, encryption means your words are protected whether you use Diction One cloud or your own server.
