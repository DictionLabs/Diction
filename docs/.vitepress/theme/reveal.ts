import type { Directive } from 'vue'

interface RevealOptions {
  delay?: number
  threshold?: number
  once?: boolean
}

let observer: IntersectionObserver | null = null
const pending = new WeakMap<Element, RevealOptions>()

function ensureObserver() {
  if (observer || typeof window === 'undefined') return observer
  observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue
        const el = entry.target as HTMLElement
        const opts = pending.get(el) ?? {}
        el.classList.add('is-visible')
        if (opts.once !== false) observer?.unobserve(el)
      }
    },
    { threshold: 0.15, rootMargin: '0px 0px -8% 0px' },
  )
  return observer
}

/**
 * v-reveal: fades an element in when it scrolls into view.
 * Usage: <div v-reveal> or <div v-reveal="{ delay: 120 }">
 * Elements with class .ld-stagger reveal their children in sequence.
 */
export const vReveal: Directive<HTMLElement, RevealOptions | undefined> = {
  mounted(el, binding) {
    const opts = binding.value ?? {}
    if (!el.classList.contains('ld-stagger')) el.classList.add('ld-reveal')
    if (opts.delay) el.style.setProperty('--ld-delay', `${opts.delay}ms`)
    if (typeof window === 'undefined') return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      el.classList.add('is-visible')
      return
    }
    pending.set(el, opts)
    ensureObserver()?.observe(el)
  },
  unmounted(el) {
    observer?.unobserve(el)
    pending.delete(el)
  },
  getSSRProps() {
    return {}
  },
}
