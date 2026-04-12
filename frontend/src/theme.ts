export const colors = {
  brand:       '#4f46e5',
  brandDark:   '#3730a3',
  brandHover:  '#4338ca',
  brandLight:  '#eef2ff',
  brandFaint:  '#f5f3ff',

  bg:          '#f7f6f3',
  surface:     '#ffffff',

  border:      '#e6e3db',
  borderLight: '#eeebe5',

  text:        '#18161a',
  textMuted:   '#6e6a74',
  textSubtle:  '#a49fa9',

  successBg:     '#ecfdf5',
  successBorder: '#a7f3d0',
  successText:   '#065f46',

  warningBg:       '#fffbeb',
  warningBorder:   '#fcd34d',
  warningText:     '#92400e',
  warningTextDark: '#78350f',

  errorBg:     '#fef2f2',
  errorBorder: '#fecaca',
  errorText:   '#991b1b',

  infoBg:     '#eff6ff',
  infoBorder: '#bfdbfe',
  infoText:   '#1d4ed8',
} as const

export const shadow = {
  sm:     '0 1px 2px rgba(0,0,0,0.05)',
  md:     '0 1px 3px rgba(0,0,0,0.07), 0 2px 8px rgba(0,0,0,0.04)',
  lg:     '0 4px 16px rgba(0,0,0,0.08), 0 1px 4px rgba(0,0,0,0.04)',
  card:   '0 0 0 1px rgba(0,0,0,0.06), 0 4px 16px rgba(0,0,0,0.08)',
  login:  '0 0 0 1px rgba(0,0,0,0.06), 0 8px 32px rgba(0,0,0,0.12), 0 2px 8px rgba(0,0,0,0.06)',
  header: '0 1px 0 #e6e3db',
} as const

export const radius = {
  sm:   4,
  md:   6,
  lg:   8,
  xl:   12,
  full: 9999,
} as const

export const font = {
  body:  "'DM Sans', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif",
  brand: "'Syne', -apple-system, sans-serif",
} as const
