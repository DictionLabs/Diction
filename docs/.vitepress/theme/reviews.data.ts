import { defineLoader } from 'vitepress'
import { createSign } from 'node:crypto'
import { readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const CACHE_FILE = join(dirname(fileURLToPath(import.meta.url)), 'reviews-cache.json')

export interface Review {
  title: string
  body: string
  author: string
}

declare const data: Review[]
export { data }

const KEY_ID = 'T43ZQ8KTCH'
const ISSUER_ID = '1c165417-1b30-448f-bfab-acfd9e96747a'
const KEY_FILE = '/home/ondrej/projects/diction/.secrets/AuthKey_T43ZQ8KTCH.p8'
const APP_ID = '6759807364'
const ENGLISH_TERRITORIES = new Set(['USA', 'GBR', 'CAN', 'AUS', 'NZL', 'IRL', 'LTU'])

// Non-English 5-star reviews translated to English by hand, keyed by reviewerNickname,
// so we can include them without waiting for more English-territory reviews to come in.
const TRANSLATIONS: Record<string, { title: string; body: string }> = {
  'Sarah-Cu': {
    title: 'Great',
    body: "Very practical app for voice dictation. I like the simple interface, the fast transcription, and the focus on privacy. Perfect for taking notes, replying to messages, or working without having to type.",
  },
}

function makeJWT(): string {
  const privateKey = readFileSync(KEY_FILE, 'utf-8')
  const header = Buffer.from(JSON.stringify({ alg: 'ES256', kid: KEY_ID, typ: 'JWT' })).toString('base64url')
  const now = Math.floor(Date.now() / 1000)
  const payload = Buffer.from(JSON.stringify({
    iss: ISSUER_ID, iat: now, exp: now + 1200, aud: 'appstoreconnect-v1'
  })).toString('base64url')
  const unsigned = `${header}.${payload}`
  const sign = createSign('SHA256')
  sign.update(unsigned)
  const sig = sign.sign({ key: privateKey, dsaEncoding: 'ieee-p1363' }, 'base64url')
  return `${unsigned}.${sig}`
}

export default defineLoader({
  async load(): Promise<Review[]> {
    try {
      if (!existsSync(KEY_FILE)) {
        console.warn('[reviews] ASC key not found, using cached reviews')
        return existsSync(CACHE_FILE) ? JSON.parse(readFileSync(CACHE_FILE, 'utf-8')).slice(0, 8) : []
      }
      const token = makeJWT()
      const url = new URL(`https://api.appstoreconnect.apple.com/v1/apps/${APP_ID}/customerReviews`)
      url.searchParams.set('limit', '50')
      url.searchParams.set('sort', '-createdDate')
      const res = await fetch(url.toString(), { headers: { Authorization: `Bearer ${token}` } })
      const json = await res.json() as any
      return (json.data ?? [])
        .filter((r: any) => r.attributes.rating === 5 && (
          ENGLISH_TERRITORIES.has(r.attributes.territory) || TRANSLATIONS[r.attributes.reviewerNickname]
        ))
        .map((r: any) => {
          const translated = TRANSLATIONS[r.attributes.reviewerNickname]
          return {
            title: translated?.title ?? r.attributes.title,
            body: translated?.body ?? r.attributes.body,
            author: r.attributes.reviewerNickname,
          }
        })
        .slice(0, 8)
    } catch (e) {
      console.warn('[reviews] Failed:', e)
      return []
    }
  },
})
