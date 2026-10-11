// One list feeds the landing FAQ and the FAQPage structured data in config.ts,
// so what agents read and what people read can never drift apart.
export const faqs: { q: string; a: string }[] = [
  {
    q: 'Is Diction free?',
    a: 'On your iPhone and on your server, yes. Free forever, no word limits, no account. Diction Cloud is the paid option at $5.99 a month or $59.99 a year, and you can try it free first.',
  },
  {
    q: 'Is it a full keyboard, or just a mic?',
    a: 'A full keyboard. QWERTY with the layout for your language, autocorrect that looks at where your finger landed, word completion and next-word suggestions, all computed on your iPhone. The mic sits above the keys, one tap away in every app.',
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
