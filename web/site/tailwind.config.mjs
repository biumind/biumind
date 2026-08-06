/** @type {import('tailwindcss').Config} */
export default {
  content: ['./src/**/*.{astro,html,js,jsx,md,mdx,ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // BiuMind 设计 token —— 与 Flutter BiuTokens 一一对应
        purple: {
          DEFAULT: '#5B5BD6',
          soft: '#EEEDFB',
          light: '#F5F4FE',
        },
        green: '#10B981',
        bg: '#FAFAFA',
        surface: {
          DEFAULT: '#FFFFFF',
          muted: '#F4F4F5',
        },
        border: {
          DEFAULT: '#E7E5E4',
          subtle: '#EEEDEC',
        },
        ink: {
          DEFAULT: '#18181B',
          secondary: '#52525B',
          muted: '#9CA3AF',
          disabled: '#C8C8C8',
        },
        error: {
          DEFAULT: '#DC2626',
          soft: '#FEE2E2',
        },
      },
      borderRadius: {
        xs: '6px',
        sm: '8px',
        md: '12px',
        lg: '16px',
        xl: '20px',
      },
      spacing: {
        // 4 px 倍数（Tailwind 默认已经覆盖；这里保留显式 token 用于沟通）
        '4.5': '18px',
      },
      fontFamily: {
        sans: [
          '-apple-system',
          'BlinkMacSystemFont',
          '"SF Pro SC"',
          '"SF Pro"',
          '"PingFang SC"',
          '"Microsoft YaHei"',
          '"Segoe UI"',
          'system-ui',
          'sans-serif',
        ],
        mono: [
          '"SF Mono"',
          'Menlo',
          'Monaco',
          'Consolas',
          '"Liberation Mono"',
          'monospace',
        ],
      },
      maxWidth: {
        content: '1200px',
        prose: '720px',
      },
      letterSpacing: {
        tightest: '-0.04em',
      },
    },
  },
  plugins: [],
};
