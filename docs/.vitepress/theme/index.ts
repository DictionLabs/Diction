import DefaultTheme from 'vitepress/theme';
import type { Theme } from 'vitepress';
import Layout from './Layout.vue';
import Icon from './Icon.vue';
import { vReveal } from './reveal';
import './custom.css';
import './landing.css';

export default {
  extends: DefaultTheme,
  Layout,
  enhanceApp({ app }) {
    app.component('Icon', Icon);
    app.directive('reveal', vReveal);
  },
} satisfies Theme;
