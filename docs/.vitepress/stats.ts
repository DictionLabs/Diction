// Public numbers fetched at build time, so every claim an agent reads is
// current and checkable. Each source falls back to the last known value if
// the fetch fails; a build never breaks on a flaky API.

export interface SiteStats {
  rating: number // US App Store average, 1 decimal
  ratingCount: number // US App Store ratings
  appVersion: string
  appReleased: string // YYYY-MM-DD
  githubStars: number
  dockerPulls: number
  hfDownloads: number // all-time, all DictionLabs models
}

const FALLBACK: SiteStats = {
  rating: 4.9,
  ratingCount: 19,
  appVersion: '14.0',
  appReleased: '2026-10-03',
  githubStars: 232,
  dockerPulls: 2400,
  hfDownloads: 8869,
}

async function json(url: string): Promise<any> {
  const res = await fetch(url, { signal: AbortSignal.timeout(8000), headers: { 'User-Agent': 'diction.one build' } })
  if (!res.ok) throw new Error(`${res.status} ${url}`)
  return res.json()
}

async function field<K extends keyof SiteStats>(key: K, get: () => Promise<SiteStats[K]>): Promise<SiteStats[K]> {
  try {
    const v = await get()
    if (v === undefined || v === null || (typeof v === 'number' && !(v > 0))) throw new Error('empty')
    return v
  } catch (e) {
    console.warn(`[stats] ${key}: using fallback (${(e as Error).message})`)
    return FALLBACK[key]
  }
}

export async function loadStats(): Promise<SiteStats> {
  const app = json('https://itunes.apple.com/lookup?id=6759807364&country=us').then((d) => d.results[0])
  app.catch(() => {})
  const [rating, ratingCount, appVersion, appReleased, githubStars, dockerPulls, hfDownloads] = await Promise.all([
    field('rating', async () => Math.round((await app).averageUserRating * 10) / 10),
    field('ratingCount', async () => (await app).userRatingCount),
    field('appVersion', async () => (await app).version),
    field('appReleased', async () => String((await app).currentVersionReleaseDate).slice(0, 10)),
    field('githubStars', async () => (await json('https://api.github.com/repos/DictionLabs/Diction')).stargazers_count),
    field('dockerPulls', async () =>
      (await json('https://hub.docker.com/v2/namespaces/dictionlabs/repositories?page_size=100')).results.reduce(
        (n: number, r: any) => n + r.pull_count,
        0,
      ),
    ),
    field('hfDownloads', async () =>
      (await json('https://huggingface.co/api/models?author=DictionLabs&expand[]=downloadsAllTime')).reduce(
        (n: number, m: any) => n + (m.downloadsAllTime ?? 0),
        0,
      ),
    ),
  ])
  return { rating, ratingCount, appVersion, appReleased, githubStars, dockerPulls, hfDownloads }
}

// "8,869" -> "8,800+": round down so a claim never overstates.
export function plus(n: number): string {
  const step = n >= 10000 ? 1000 : n >= 1000 ? 100 : 10
  return `${(Math.floor(n / step) * step).toLocaleString('en-US')}+`
}
