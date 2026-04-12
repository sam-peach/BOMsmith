import { colors, font } from '../theme'

export function LogoMark({ size = 32 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none" aria-hidden="true">
      <rect width="32" height="32" rx="8" fill={colors.brand} />
      {/* connection lines */}
      <line x1="13.5" y1="14.2" x2="21.5" y2="9.8"  stroke="white" strokeWidth="1.5" strokeLinecap="round" opacity="0.5" />
      <line x1="13.5" y1="17.8" x2="21.5" y2="22.2" stroke="white" strokeWidth="1.5" strokeLinecap="round" opacity="0.5" />
      {/* hub node */}
      <circle cx="10.5" cy="16" r="4"   fill="white" />
      {/* satellite nodes */}
      <circle cx="23"   cy="9"  r="2.5" fill="white" opacity="0.85" />
      <circle cx="23"   cy="23" r="2.5" fill="white" opacity="0.85" />
    </svg>
  )
}

export function LogoWordmark({
  size     = 28,
  inverted = false,
}: {
  size?:     number
  inverted?: boolean
}) {
  const color    = inverted ? '#ffffff' : colors.text
  const fontSize = Math.round(size * 0.72)

  return (
    <span style={{
      fontFamily:    font.brand,
      fontSize,
      letterSpacing: '-0.04em',
      lineHeight:    1,
      userSelect:    'none',
    }}>
      <span style={{ fontWeight: 700, color }}>BOMsmith</span>
    </span>
  )
}
