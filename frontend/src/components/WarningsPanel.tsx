import { colors, radius } from '../theme'

interface Props {
  warnings: string[]
}

export default function WarningsPanel({ warnings }: Props) {
  if (!warnings || warnings.length === 0) return null
  return (
    <div style={{
      background:   colors.warningBg,
      border:       `1px solid ${colors.warningBorder}`,
      borderRadius: radius.md,
      padding:      '12px 16px',
      marginBottom: 12,
    }}>
      <strong style={{ display: 'block', marginBottom: 8, color: colors.warningText, fontSize: 14 }}>
        Warnings &amp; Ambiguities
      </strong>
      <ul style={{ margin: 0, paddingLeft: 20 }}>
        {warnings.map((w, i) => (
          <li key={i} style={{ fontSize: 13, color: colors.warningTextDark, marginBottom: 4 }}>
            {w}
          </li>
        ))}
      </ul>
    </div>
  )
}
