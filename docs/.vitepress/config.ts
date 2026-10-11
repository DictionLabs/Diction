import { defineConfig } from 'vitepress';
import llmstxt from 'vitepress-plugin-llms';
import { faqs } from './theme/landing/faq';

const SITE_URL = 'https://diction.one';
const SITE_NAME = 'Diction';
const SITE_TITLE = 'Diction - Voice Keyboard for iPhone';
const DEFAULT_DESCRIPTION =
  'iPhone keyboard built for voice. Dictate in any app or type on a full QWERTY keyboard. Free on-device and self-hosted speech to text, or Diction Cloud. No word limits.';
const DEFAULT_KEYWORDS =
  'voice keyboard, speech to text iPhone, dictation keyboard, iPhone keyboard, self-hosted speech to text, self-hosted whisper, offline dictation, on-device transcription, Gboard alternative, SwiftKey alternative, Wispr Flow alternative, QWERTY keyboard, autocorrect, open source server';
// The summary every AI agent reads first in /llms.txt. Facts only, same as the site.
const LLMS_DETAILS = `Key facts:

- Diction is a third-party keyboard for iPhone, written from the ground up for voice. It is also a full QWERTY typing keyboard with layouts per language, on-device autocorrect, word completion and next-word suggestions.
- Dictate in any app: tap the mic in the Diction Bar above the keys, speak, and clean text is inserted at the cursor.
- Three places to run speech to text, picked in the app:
  - On-device: free, offline, audio never leaves the iPhone. Every on-device model is free.
  - Self-hosted: free. Open-source server on GitHub (DictionLabs/Diction), started with docker compose, paired by QR code. Works with any OpenAI-compatible speech endpoint (Whisper included) and any OpenAI-compatible LLM (Ollama included) for Writing Tools. No account, no limits.
  - Diction Cloud: subscription ($5.99/month or $59.99/year in the US, free trial). Speech is transcribed on GPU servers Diction runs itself. Live transcription and every Writing Tool included.
- Writing Tools: removes filler words, fixes grammar and punctuation, formats lists and paragraphs, spells your names via My Words, and edits existing text by voice ("make it shorter", "translate to Spanish").
- 99 dictation languages. No word limits. Audio is never stored in any mode. The Diction app contains no analytics and no tracking code.
- Made by Diction Labs. App Store: https://apps.apple.com/app/id6759807364 · Server: https://github.com/DictionLabs/Diction · Models: https://huggingface.co/DictionLabs
- Fact sheet: https://diction.one/facts.md`;
const OG_IMAGE = `${SITE_URL}/og-image.png`;

function getBreadcrumbList(relativePath: string, title: string): string {
  const cleanPath = relativePath.replace(/\.md$/, '').replace(/\/index$/, '');
  const parts = cleanPath.split('/').filter(Boolean);
  const sectionLabels: Record<string, string> = { features: 'Features' };
  const items: object[] = [{ '@type': 'ListItem', position: 1, name: 'Diction', item: SITE_URL }];
  if (parts.length === 1) {
    items.push({ '@type': 'ListItem', position: 2, name: title, item: `${SITE_URL}/${parts[0]}` });
  } else if (parts.length >= 2) {
    items.push({ '@type': 'ListItem', position: 2, name: sectionLabels[parts[0]] || parts[0], item: `${SITE_URL}/${parts[0]}` });
    items.push({ '@type': 'ListItem', position: 3, name: title, item: `${SITE_URL}/${cleanPath}` });
  }
  return JSON.stringify({ '@context': 'https://schema.org', '@type': 'BreadcrumbList', itemListElement: items });
}

export default defineConfig({
  vite: {
    plugins: [
      llmstxt({
        customTemplateVariables: {
          title: 'Diction: iPhone keyboard for voice and typing',
          description: DEFAULT_DESCRIPTION,
          details: LLMS_DETAILS,
        },
      }),
    ],
  },

  title: SITE_NAME,
  titleTemplate: `:title | ${SITE_NAME}`,
  description: DEFAULT_DESCRIPTION,
  // Clean URLs everywhere (links, sitemap) so they match the canonical tags.
  // Caddy serves /page from page.html and 301s old .html URLs.
  cleanUrls: true,
  sitemap: { hostname: SITE_URL },
  lastUpdated: true,

  // Dynamic page-level SEO via transformPageData
  transformPageData(pageData) {
    // Build canonical URL - match VitePress output format
    const canonicalUrl = `${SITE_URL}/${pageData.relativePath}`
      .replace(/index\.md$/, '')
      .replace(/\.md$/, '');

    // Homepage gets the full branded title, other pages get "Page | Diction"
    const isHomePage = pageData.relativePath === 'index.md';
    const pageTitle = pageData.frontmatter.title || pageData.title;
    const fullTitle = isHomePage
      ? pageTitle || SITE_TITLE
      : pageTitle
        ? `${pageTitle} | ${SITE_NAME}`
        : SITE_NAME;
    const pageDescription =
      pageData.frontmatter.description || pageData.description || DEFAULT_DESCRIPTION;
    const pageKeywords = pageData.frontmatter.keywords || DEFAULT_KEYWORDS;

    // Add dynamic head tags
    pageData.frontmatter.head ??= [];
    pageData.frontmatter.head.push(
      // Canonical URL - critical for SEO
      ['link', { rel: 'canonical', href: canonicalUrl }],
      // Robots
      ['meta', { name: 'robots', content: 'index, follow' }],
      // Keywords
      ['meta', { name: 'keywords', content: pageKeywords }],
      // Dynamic Open Graph
      ['meta', { property: 'og:title', content: fullTitle }],
      ['meta', { property: 'og:description', content: pageDescription }],
      ['meta', { property: 'og:url', content: canonicalUrl }],
      // Dynamic Twitter
      ['meta', { name: 'twitter:title', content: fullTitle }],
      ['meta', { name: 'twitter:description', content: pageDescription }],
    );

    // BreadcrumbList for inner pages
    if (!isHomePage) {
      pageData.frontmatter.head.push(
        ['script', { type: 'application/ld+json' }, getBreadcrumbList(pageData.relativePath, pageTitle || '')],
      );
    }
  },

  head: [
    // Preconnect for performance
    ['link', { rel: 'preconnect', href: 'https://www.googletagmanager.com' }],
    // Self-hosted display faces (see custom.css). Preloaded so headings don't swap late.
    ['link', { rel: 'preload', href: '/fonts/LibreFranklin-latin-var.woff2', as: 'font', type: 'font/woff2', crossorigin: '' }],
    ['link', { rel: 'preload', href: '/fonts/BigShouldersInlineText-ExtraBold.woff2', as: 'font', type: 'font/woff2', crossorigin: '' }],
    ['link', { rel: 'dns-prefetch', href: 'https://www.googletagmanager.com' }],
    // Google Analytics 4
    ['script', { async: '', src: 'https://www.googletagmanager.com/gtag/js?id=G-PCV64Y7GFM' }],
    [
      'script',
      {},
      `window.dataLayer = window.dataLayer || [];
function gtag(){dataLayer.push(arguments);}
gtag('js', new Date());
gtag('config', 'G-PCV64Y7GFM');`,
    ],
    // Favicons - generated from app-icon.png
    ['link', { rel: 'icon', type: 'image/png', sizes: '32x32', href: '/favicon-32.png' }],
    ['link', { rel: 'icon', href: '/favicon.ico', sizes: '48x48' }],
    ['link', { rel: 'apple-touch-icon', href: '/apple-touch-icon.png' }],
    // Apple Smart Banner - shows "Open in App Store" bar on Safari
    ['meta', { name: 'apple-itunes-app', content: 'app-id=6759807364' }],
    // Theme color for mobile browsers
    ['meta', { name: 'theme-color', content: '#007AFF' }],
    // Language
    ['meta', { property: 'og:locale', content: 'en_US' }],
    // Structured data - SoftwareApplication
    [
      'script',
      { type: 'application/ld+json' },
      JSON.stringify({
        '@context': 'https://schema.org',
        '@type': 'SoftwareApplication',
        name: SITE_NAME,
        alternateName: 'diction',
        url: SITE_URL,
        logo: `${SITE_URL}/app-icon.png`,
        image: OG_IMAGE,
        screenshot: OG_IMAGE,
        description: DEFAULT_DESCRIPTION,
        applicationCategory: 'UtilitiesApplication',
        applicationSubCategory: 'Productivity',
        operatingSystem: 'iOS',
        softwareRequirements: 'iOS 17+',
        downloadUrl: 'https://apps.apple.com/app/id6759807364',
        installUrl: 'https://apps.apple.com/app/id6759807364',
        releaseNotes: 'https://github.com/DictionLabs/Diction/releases',
        keywords:
          'voice keyboard, speech to text, dictation, self-hosted, whisper, iOS keyboard, transcription, privacy',
        featureList: [
          'Full QWERTY keyboard written from the ground up, with layouts per language',
          'On-device autocorrect, word completion and next-word suggestions',
          'Voice dictation in every app from the Diction Bar above the keys',
          'Free on-device speech to text, works offline',
          'Free self-hosted speech to text with an open-source Docker server, paired by QR code',
          'Bring any OpenAI-compatible speech or LLM endpoint, Whisper and Ollama included',
          'Diction Cloud: speech to text on GPU servers Diction runs itself, Live transcription',
          'Writing Tools: filler removal, grammar, formatting, voice commands, My Words',
          '99 dictation languages, no word limits, audio never stored',
          'No analytics or tracking code in the app',
        ],
        offers: [
          {
            '@type': 'Offer',
            name: 'On-device and self-hosted',
            price: '0',
            priceCurrency: 'USD',
            availability: 'https://schema.org/InStock',
          },
          {
            '@type': 'Offer',
            name: 'Diction Cloud monthly',
            price: '5.99',
            priceCurrency: 'USD',
            availability: 'https://schema.org/InStock',
          },
          {
            '@type': 'Offer',
            name: 'Diction Cloud annual',
            price: '59.99',
            priceCurrency: 'USD',
            availability: 'https://schema.org/InStock',
          },
        ],
        author: {
          '@type': 'Organization',
          name: 'Diction Labs',
          url: 'https://diction.one',
        },
        maintainer: {
          '@type': 'Organization',
          name: 'Diction Labs',
          url: 'https://diction.one',
        },
      }),
    ],
    // Structured data - FAQPage
    [
      'script',
      { type: 'application/ld+json' },
      JSON.stringify({
        '@context': 'https://schema.org',
        '@type': 'FAQPage',
        mainEntity: faqs.map(({ q, a }) => ({
          '@type': 'Question',
          name: q,
          acceptedAnswer: { '@type': 'Answer', text: a },
        })),
      }),
    ],
    // Structured data - WebSite with SearchAction
    [
      'script',
      { type: 'application/ld+json' },
      JSON.stringify({
        '@context': 'https://schema.org',
        '@type': 'WebSite',
        name: SITE_NAME,
        url: SITE_URL,
        description: DEFAULT_DESCRIPTION,
        potentialAction: {
          '@type': 'SearchAction',
          target: {
            '@type': 'EntryPoint',
            urlTemplate: `${SITE_URL}/?q={search_term_string}`,
          },
          'query-input': 'required name=search_term_string',
        },
      }),
    ],
    // Structured data - Organization
    [
      'script',
      { type: 'application/ld+json' },
      JSON.stringify({
        '@context': 'https://schema.org',
        '@type': 'Organization',
        name: 'Diction Labs',
        alternateName: SITE_NAME,
        url: SITE_URL,
        logo: `${SITE_URL}/app-icon.png`,
        sameAs: [
          'https://github.com/DictionLabs',
          'https://huggingface.co/DictionLabs',
          'https://apps.apple.com/app/id6759807364',
          'https://x.com/diction_one',
          'https://www.reddit.com/r/dictionapp',
        ],
      }),
    ],
    // Static Open Graph (fallbacks - dynamic ones override these)
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: SITE_NAME }],
    ['meta', { property: 'og:image', content: OG_IMAGE }],
    ['meta', { property: 'og:image:width', content: '1200' }],
    ['meta', { property: 'og:image:height', content: '630' }],
    // Static Twitter (fallbacks)
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:image', content: OG_IMAGE }],
    ['meta', { name: 'twitter:site', content: '@diction_one' }],
    ['meta', { name: 'twitter:creator', content: '@diction_one' }],
  ],

  themeConfig: {
    siteTitle: 'Diction',

    search: {
      provider: 'local',
    },

    nav: [
      // activeMatch never matches: an in-page anchor should not look like the current page
      { text: 'Pricing', link: '/#pricing', activeMatch: '^/pricing-never$' },
      { text: 'Docs', link: '/features/' },
      { text: 'Support', link: '/support' },
      {
        text: 'Get the app',
        link: 'https://apps.apple.com/app/id6759807364',
      },
    ],

    sidebar: {
      '/': [
        {
          text: 'Diction at a Glance',
          items: [{ text: 'Facts', link: '/facts' }],
        },
        {
          text: 'Alternatives',
          items: [
            { text: 'Gboard Alternative', link: '/alternatives/gboard' },
            { text: 'SwiftKey Alternative', link: '/alternatives/swiftkey' },
          ],
        },
        {
          text: 'Features',
          items: [
            { text: 'Overview', link: '/features/' },
            { text: 'Context-Aware Text Editing', link: '/features/context-aware' },
            {
              text: 'Writing Tools',
              link: '/features/writing-tools',
              items: [
                { text: 'Writing Style', link: '/features/writing-tools#writing-style' },
                { text: 'Tones', link: '/features/tone-presets' },
                { text: 'My Words', link: '/features/my-words' },
              ],
            },
            { text: 'Transcription History', link: '/features/history' },
          ],
        },
        {
          text: 'Transcription Modes',
          items: [
            { text: 'On-Device', link: '/on-device' },
            { text: 'Self-Hosted', link: '/self-hosted' },
            { text: 'Diction One (Cloud)', link: '/cloud' },
          ],
        },
        {
          text: 'Security',
          items: [
            { text: 'Privacy First', link: '/privacy-first' },
            { text: 'Encryption', link: '/encryption' },
          ],
        },
        {
          text: 'Guides',
          items: [
            { text: 'Keyboard Preferences', link: '/features/preferences' },
            { text: 'Self-Hosting Setup', link: '/features/self-hosting-setup' },
            { text: 'Use Your Own Model', link: '/features/custom-model' },
          ],
        },
        {
          text: 'Help',
          items: [
            { text: 'Support', link: '/support' },
            { text: 'Release Notes', link: '/releases' },
          ],
        },
      ],
    },

    socialLinks: [
      { icon: 'github', link: 'https://github.com/DictionLabs/Diction' },
      { icon: 'x', link: 'https://x.com/diction_one' },
      { icon: { svg: '<svg role="img" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path fill="currentColor" d="M12.025 1.13c-5.77 0-10.449 4.647-10.449 10.378 0 1.112.178 2.181.503 3.185.064-.222.203-.444.416-.577a.96.96 0 0 1 .524-.15c.293 0 .584.124.84.284.278.173.48.408.71.694.226.282.458.611.684.951v-.014c.017-.324.106-.622.264-.874s.403-.487.762-.543c.3-.047.596.06.787.203s.31.313.4.467c.15.257.212.468.233.542.01.026.653 1.552 1.657 2.54.616.605 1.01 1.223 1.082 1.912.055.537-.096 1.059-.38 1.572.637.121 1.294.187 1.967.187.657 0 1.298-.063 1.921-.178-.287-.517-.44-1.041-.384-1.581.07-.69.465-1.307 1.081-1.913 1.004-.987 1.647-2.513 1.657-2.539.021-.074.083-.285.233-.542.09-.154.208-.323.4-.467a1.08 1.08 0 0 1 .787-.203c.359.056.604.29.762.543s.247.55.265.874v.015c.225-.34.457-.67.683-.952.23-.286.432-.52.71-.694.257-.16.547-.284.84-.285a.97.97 0 0 1 .524.151c.228.143.373.388.43.625l.006.04a10.3 10.3 0 0 0 .534-3.273c0-5.731-4.678-10.378-10.449-10.378M8.327 6.583a1.5 1.5 0 0 1 .713.174 1.487 1.487 0 0 1 .617 2.013c-.183.343-.762-.214-1.102-.094-.38.134-.532.914-.917.71a1.487 1.487 0 0 1 .69-2.803m7.486 0a1.487 1.487 0 0 1 .689 2.803c-.385.204-.536-.576-.916-.71-.34-.12-.92.437-1.103.094a1.487 1.487 0 0 1 .617-2.013 1.5 1.5 0 0 1 .713-.174m-10.68 1.55a.96.96 0 1 1 0 1.921.96.96 0 0 1 0-1.92m13.838 0a.96.96 0 1 1 0 1.92.96.96 0 0 1 0-1.92M8.489 11.458c.588.01 1.965 1.157 3.572 1.164 1.607-.007 2.984-1.155 3.572-1.164.196-.003.305.12.305.454 0 .886-.424 2.328-1.563 3.202-.22-.756-1.396-1.366-1.63-1.32q-.011.001-.02.006l-.044.026-.01.008-.03.024q-.018.017-.035.036l-.032.04a1 1 0 0 0-.058.09l-.014.025q-.049.088-.11.19a1 1 0 0 1-.083.116 1.2 1.2 0 0 1-.173.18q-.035.029-.075.058a1.3 1.3 0 0 1-.251-.243 1 1 0 0 1-.076-.107c-.124-.193-.177-.363-.337-.444-.034-.016-.104-.008-.2.022q-.094.03-.216.087-.06.028-.125.063l-.13.074q-.067.04-.136.086a3 3 0 0 0-.135.096 3 3 0 0 0-.26.219 2 2 0 0 0-.12.121 2 2 0 0 0-.106.128l-.002.002a2 2 0 0 0-.09.132l-.001.001a1.2 1.2 0 0 0-.105.212q-.013.036-.024.073c-1.139-.875-1.563-2.317-1.563-3.203 0-.334.109-.457.305-.454m.836 10.354c.824-1.19.766-2.082-.365-3.194-1.13-1.112-1.789-2.738-1.789-2.738s-.246-.945-.806-.858-.97 1.499.202 2.362c1.173.864-.233 1.45-.685.64-.45-.812-1.683-2.896-2.322-3.295s-1.089-.175-.938.647 2.822 2.813 2.562 3.244-1.176-.506-1.176-.506-2.866-2.567-3.49-1.898.473 1.23 2.037 2.16c1.564.932 1.686 1.178 1.464 1.53s-3.675-2.511-4-1.297c-.323 1.214 3.524 1.567 3.287 2.405-.238.839-2.71-1.587-3.216-.642-.506.946 3.49 2.056 3.522 2.064 1.29.33 4.568 1.028 5.713-.624m5.349 0c-.824-1.19-.766-2.082.365-3.194 1.13-1.112 1.789-2.738 1.789-2.738s.246-.945.806-.858.97 1.499-.202 2.362c-1.173.864.233 1.45.685.64.451-.812 1.683-2.896 2.322-3.295s1.089-.175.938.647-2.822 2.813-2.562 3.244 1.176-.506 1.176-.506 2.866-2.567 3.49-1.898-.473 1.23-2.037 2.16c-1.564.932-1.686 1.178-1.464 1.53s3.675-2.511 4-1.297c.323 1.214-3.524 1.567-3.287 2.405.238.839 2.71-1.587 3.216-.642.506.946-3.49 2.056-3.522 2.064-1.29.33-4.568 1.028-5.713-.624"/></svg>' }, link: 'https://huggingface.co/DictionLabs', ariaLabel: 'Hugging Face' },
      { icon: { svg: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="currentColor" d="M12 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0m5.01 4.744c.688 0 1.25.561 1.25 1.249a1.25 1.25 0 0 1-2.498.056l-2.597-.547-.8 3.747c1.824.07 3.48.632 4.674 1.488.308-.309.73-.491 1.207-.491.968 0 1.754.786 1.754 1.754 0 .716-.435 1.333-1.01 1.614a3.111 3.111 0 0 1 .042.52c0 2.694-3.13 4.87-7.004 4.87-3.874 0-7.004-2.176-7.004-4.87 0-.183.015-.366.043-.534A1.748 1.748 0 0 1 4.028 12c0-.968.786-1.754 1.754-1.754.463 0 .898.196 1.207.49 1.207-.883 2.878-1.43 4.744-1.487l.885-4.182a.342.342 0 0 1 .14-.197.35.35 0 0 1 .238-.042l2.906.617a1.214 1.214 0 0 1 1.108-.701M9.25 12C8.561 12 8 12.562 8 13.25c0 .687.561 1.248 1.25 1.248.687 0 1.248-.561 1.248-1.249 0-.688-.561-1.249-1.249-1.249m5.5 0c-.687 0-1.248.561-1.248 1.25 0 .687.561 1.248 1.249 1.248.688 0 1.249-.561 1.249-1.249 0-.687-.562-1.249-1.25-1.249m-5.466 3.99a.327.327 0 0 0-.231.094.33.33 0 0 0 0 .463c.842.842 2.484.913 2.961.913.477 0 2.105-.056 2.961-.913a.361.361 0 0 0 .029-.463.33.33 0 0 0-.464 0c-.547.533-1.684.73-2.512.73-.828 0-1.979-.196-2.512-.73a.326.326 0 0 0-.232-.095"/></svg>' }, link: 'https://www.reddit.com/r/dictionapp' },
    ],


    lastUpdated: {
      text: 'Last updated',
    },
  },
});
