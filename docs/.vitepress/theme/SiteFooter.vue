<template>
  <footer class="site-footer">
    <div class="footer-container">
      <div class="footer-columns">
        <div class="footer-col footer-brand">
          <a href="/" class="brand-link">
            <img src="/app-icon.png" alt="Diction" class="brand-icon" width="40" height="40" />
            <span class="brand-name">Diction</span>
          </a>
          <p class="brand-tagline">Voice keyboard for iPhone.</p>
          <div class="social-icons">
            <a href="https://github.com/DictionLabs/Diction" target="_blank" rel="noopener" aria-label="GitHub">
              <img src="/icon-github.svg" alt="" width="20" height="20" class="social-icon" />
            </a>
            <a href="https://x.com/diction_one" target="_blank" rel="noopener" aria-label="X">
              <img src="/icon-x.svg" alt="" width="18" height="18" class="social-icon" />
            </a>
            <a href="https://www.reddit.com/r/dictionapp" target="_blank" rel="noopener" aria-label="Reddit">
              <img src="/icon-reddit.svg" alt="" width="20" height="20" class="social-icon" />
            </a>
          </div>
        </div>

        <div class="footer-col">
          <h4>Product</h4>
          <ul>
            <li><a href="/features/"><img src="/icon-features.svg" alt="" class="link-icon" />Features</a></li>
            <li><a href="/on-device"><img src="/icon-phone.svg" alt="" class="link-icon" />On-Device</a></li>
            <li><a href="/self-hosted"><img src="/icon-server.svg" alt="" class="link-icon" />Self-Hosted</a></li>
            <li><a href="/cloud"><img src="/icon-cloud.svg" alt="" class="link-icon" />Diction One</a></li>
            <li><a href="/encryption"><img src="/icon-lock.svg" alt="" class="link-icon" />Encryption</a></li>
          </ul>
        </div>

        <div class="footer-col">
          <h4>Resources</h4>
          <ul>
            <li><a href="/support"><img src="/icon-lifebuoy.svg" alt="" class="link-icon" />Support</a></li>
            <li><a href="https://github.com/DictionLabs/Diction" target="_blank" rel="noopener"><img src="/icon-github.svg" alt="" class="link-icon" />GitHub</a></li>
            <li><a href="https://www.reddit.com/r/dictionapp" target="_blank" rel="noopener"><img src="/icon-reddit.svg" alt="" class="link-icon" />Reddit</a></li>
            <li><a href="https://x.com/diction_one" target="_blank" rel="noopener"><img src="/icon-x.svg" alt="" class="link-icon" />X / Twitter</a></li>
          </ul>
        </div>

        <div class="footer-col">
          <h4>Legal</h4>
          <ul>
            <li><a href="/privacy"><img src="/icon-shield.svg" alt="" class="link-icon" />Privacy Policy</a></li>
            <li><a href="/terms"><img src="/icon-scale.svg" alt="" class="link-icon" />Terms of Service</a></li>
          </ul>
        </div>
      </div>

      <div class="footer-bottom">
        <span>&copy; {{ year }} Diction Labs</span>
      </div>
    </div>
  </footer>
</template>

<script setup lang="ts">
const year = new Date().getFullYear();
</script>

<style>
/* Footer tokens -- unscoped so :root/.dark work */
:root {
  --footer-bg: #f6f8fa;
  --footer-border: var(--vp-c-divider);
  --footer-heading: #1a1a1a;
  --footer-link: #4a4a4a;
  --footer-link-hover: var(--diction-blue);
  --footer-divider: #d1d5db;
  --footer-muted: #6e6e6e;
}

.dark {
  --footer-bg: #141416;
  --footer-border: rgba(255, 255, 255, 0.06);
  --footer-heading: #e8e8e8;
  --footer-link: #a0a0a0;
  --footer-link-hover: #4DA3FF;
  --footer-divider: rgba(255, 255, 255, 0.08);
  --footer-muted: #707070;
}
</style>

<style scoped>
.site-footer {
  border-top: 1px solid var(--footer-border);
  background: var(--footer-bg);
  padding: 48px 24px 32px;
}

.footer-container {
  max-width: 1152px;
  margin: 0 auto;
}

.footer-columns {
  display: grid;
  grid-template-columns: 1.4fr 1fr 1fr 1fr;
  gap: 40px;
}

/* Brand column */
.brand-link {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  text-decoration: none;
}

.brand-icon {
  border-radius: 22.5%;
}

.brand-name {
  font-family: 'BigShouldersInlineText', sans-serif;
  font-weight: 800;
  font-size: 24px;
  color: var(--footer-heading);
  letter-spacing: 0.5px;
}

.brand-tagline {
  margin: 12px 0 0;
  font-size: 14px;
  color: var(--footer-muted);
  line-height: 1.5;
}

/* Social icons row */
.social-icons {
  display: flex;
  gap: 12px;
  margin-top: 16px;
}

.social-icons a {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 6px;
  transition: background 0.15s;
}

.social-icons a:hover {
  background: rgba(0, 0, 0, 0.06);
}

.dark .social-icons a:hover {
  background: rgba(255, 255, 255, 0.08);
}

.social-icon {
  opacity: 0.6;
  transition: opacity 0.15s;
}

.social-icons a:hover .social-icon {
  opacity: 1;
}

.dark .social-icon {
  filter: invert(1);
}

/* Column headings */
.footer-col h4 {
  font-size: 13px;
  font-weight: 600;
  color: var(--footer-heading);
  margin: 0 0 16px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

/* Links */
.footer-col ul {
  list-style: none;
  margin: 0;
  padding: 0;
}

.footer-col li {
  margin: 0 0 10px;
}

.footer-col a {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 14px;
  color: var(--footer-link);
  text-decoration: none;
  transition: color 0.15s;
}

.footer-col a:hover {
  color: var(--footer-link-hover);
}

/* Link icons */
.link-icon {
  width: 16px;
  height: 16px;
  opacity: 0.45;
  flex-shrink: 0;
  transition: opacity 0.15s;
}

.footer-col a:hover .link-icon {
  opacity: 0.7;
}

.dark .link-icon {
  filter: invert(1);
}

/* Bottom bar */
.footer-bottom {
  margin-top: 40px;
  padding-top: 24px;
  border-top: 1px solid var(--footer-divider);
  font-size: 13px;
  color: var(--footer-muted);
}

@media (max-width: 768px) {
  .footer-columns {
    grid-template-columns: 1fr 1fr;
    gap: 32px;
  }
}

@media (max-width: 480px) {
  .site-footer {
    padding: 28px 20px 20px;
  }

  .footer-columns {
    grid-template-columns: 1fr 1fr;
    gap: 28px 20px;
  }

  /* Brand spans full width */
  .footer-brand {
    grid-column: 1 / -1;
  }

  /* Legal spans full width with links in a horizontal row */
  .footer-col:last-child {
    grid-column: 1 / -1;
  }

  .footer-col:last-child ul {
    display: flex;
    gap: 20px;
    flex-wrap: wrap;
  }

  .footer-col:last-child li {
    margin: 0;
  }
}
</style>
