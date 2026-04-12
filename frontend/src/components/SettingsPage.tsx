import { useState } from 'react'
import { changePassword } from '../api/client'
import { LogoWordmark } from './Logo'
import { colors, font, radius, shadow } from '../theme'

interface Props {
  onBack: () => void
}

export default function SettingsPage({ onBack }: Props) {
  const [currentPassword,  setCurrentPassword]  = useState('')
  const [newPassword,      setNewPassword]      = useState('')
  const [confirmPassword,  setConfirmPassword]  = useState('')
  const [saving,           setSaving]           = useState(false)
  const [error,            setError]            = useState<string | null>(null)
  const [success,          setSuccess]          = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setSuccess(false)

    if (newPassword !== confirmPassword) {
      setError('New passwords do not match.')
      return
    }
    if (newPassword.length < 8) {
      setError('New password must be at least 8 characters.')
      return
    }

    setSaving(true)
    try {
      await changePassword(currentPassword, newPassword)
      setSuccess(true)
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ fontFamily: font.body, minHeight: '100vh', background: colors.bg, color: colors.text }}>

      {/* ── Nav (mirrors App header) ─────────────────────────────────────── */}
      <header style={navHeader}>
        <div style={navInner}>
          <LogoWordmark size={28} />
          <button style={ghostBtn} onClick={onBack}>← Back</button>
        </div>
      </header>

      {/* ── Content ──────────────────────────────────────────────────────── */}
      <main style={mainStyle}>

        <div style={{ marginBottom: 28 }}>
          <h1 style={{ margin: '0 0 4px', fontSize: 20, fontWeight: 600, letterSpacing: '-0.02em' }}>
            Settings
          </h1>
          <p style={{ margin: 0, color: colors.textMuted, fontSize: 14 }}>
            Manage your account settings.
          </p>
        </div>

        <section style={card}>
          <h2 style={{ margin: '0 0 20px', fontSize: 15, fontWeight: 600, color: colors.text }}>
            Change Password
          </h2>

          {success && (
            <div style={successBanner}>Password updated successfully.</div>
          )}
          {error && (
            <div style={errorBanner}>{error}</div>
          )}

          <form onSubmit={handleSubmit} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <Field
              label="Current password"
              id="currentPassword"
              value={currentPassword}
              onChange={setCurrentPassword}
              autoComplete="current-password"
            />
            <Field
              label="New password"
              id="newPassword"
              value={newPassword}
              onChange={setNewPassword}
              autoComplete="new-password"
            />
            <Field
              label="Confirm new password"
              id="confirmPassword"
              value={confirmPassword}
              onChange={setConfirmPassword}
              autoComplete="new-password"
            />
            <div>
              <button type="submit" style={primaryBtn} disabled={saving}>
                {saving ? 'Saving…' : 'Update password'}
              </button>
            </div>
          </form>
        </section>

      </main>
    </div>
  )
}

function Field({
  label,
  id,
  value,
  onChange,
  autoComplete,
}: {
  label:         string
  id:            string
  value:         string
  onChange:      (v: string) => void
  autoComplete?: string
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label htmlFor={id} style={{ fontSize: 13, fontWeight: 500, color: colors.text }}>
        {label}
      </label>
      <input
        className="field-input"
        id={id}
        type="password"
        value={value}
        onChange={e => onChange(e.target.value)}
        autoComplete={autoComplete}
        required
      />
    </div>
  )
}

// ── Styles ──────────────────────────────────────────────────────────────────

const navHeader: React.CSSProperties = {
  position:   'sticky',
  top:        0,
  zIndex:     100,
  background: colors.surface,
  boxShadow:  shadow.header,
}

const navInner: React.CSSProperties = {
  maxWidth:       1200,
  margin:         '0 auto',
  padding:        '0 24px',
  height:         58,
  display:        'flex',
  alignItems:     'center',
  justifyContent: 'space-between',
}

const mainStyle: React.CSSProperties = {
  maxWidth: 1200,
  margin:   '0 auto',
  padding:  '36px 24px 72px',
}

const card: React.CSSProperties = {
  maxWidth:     480,
  background:   colors.surface,
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.lg,
  padding:      '24px 28px',
  boxShadow:    shadow.sm,
}

const primaryBtn: React.CSSProperties = {
  padding:      '9px 20px',
  background:   colors.brand,
  color:        '#fff',
  border:       'none',
  borderRadius: radius.md,
  cursor:       'pointer',
  fontSize:     14,
  fontWeight:   600,
  fontFamily:   font.body,
}

const ghostBtn: React.CSSProperties = {
  padding:      '7px 14px',
  background:   'transparent',
  color:        colors.textMuted,
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.md,
  cursor:       'pointer',
  fontSize:     14,
  fontFamily:   font.body,
}

const successBanner: React.CSSProperties = {
  background:   colors.successBg,
  color:        colors.successText,
  border:       `1px solid ${colors.successBorder}`,
  padding:      '10px 14px',
  borderRadius: radius.md,
  fontSize:     14,
  marginBottom: 16,
}

const errorBanner: React.CSSProperties = {
  background:   colors.errorBg,
  color:        colors.errorText,
  border:       `1px solid ${colors.errorBorder}`,
  padding:      '10px 14px',
  borderRadius: radius.md,
  fontSize:     14,
  marginBottom: 16,
}
