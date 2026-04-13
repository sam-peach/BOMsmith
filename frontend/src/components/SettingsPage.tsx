import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { changePassword, createInvite } from '../api/client'
import { colors, font, radius, shadow } from '../theme'

export default function SettingsPage() {
  const navigate = useNavigate()
  const [currentPassword,  setCurrentPassword]  = useState('')
  const [newPassword,      setNewPassword]      = useState('')
  const [confirmPassword,  setConfirmPassword]  = useState('')
  const [saving,           setSaving]           = useState(false)
  const [error,            setError]            = useState<string | null>(null)
  const [success,          setSuccess]          = useState(false)

  const [inviteUrl,        setInviteUrl]        = useState<string | null>(null)
  const [inviteExpiry,     setInviteExpiry]     = useState<string | null>(null)
  const [inviteLoading,    setInviteLoading]    = useState(false)
  const [inviteError,      setInviteError]      = useState<string | null>(null)
  const [inviteCopied,     setInviteCopied]     = useState(false)

  async function handleCreateInvite() {
    setInviteError(null)
    setInviteUrl(null)
    setInviteLoading(true)
    try {
      const { inviteUrl: path, expiresAt } = await createInvite()
      const fullUrl = `${window.location.origin}${path}`
      setInviteUrl(fullUrl)
      setInviteExpiry(new Date(expiresAt).toLocaleDateString(undefined, { dateStyle: 'medium' }))
    } catch (e) {
      setInviteError((e as Error).message)
    } finally {
      setInviteLoading(false)
    }
  }

  function handleCopyInvite() {
    if (!inviteUrl) return
    navigator.clipboard.writeText(inviteUrl).then(() => {
      setInviteCopied(true)
      setTimeout(() => setInviteCopied(false), 2000)
    })
  }

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
    <main style={mainStyle}>

      <div style={{ marginBottom: 28 }}>
        <button style={backBtn} onClick={() => navigate('/')}>← Back</button>
        <h1 style={{ margin: '0 0 4px', fontSize: 20, fontWeight: 600, letterSpacing: '-0.02em' }}>
          Settings
        </h1>
        <p style={{ margin: 0, color: colors.textMuted, fontSize: 14 }}>
          Manage your account settings.
        </p>
      </div>

        <section style={{ ...card, marginBottom: 16 }}>
          <h2 style={{ margin: '0 0 12px', fontSize: 15, fontWeight: 600, color: colors.text }}>
            Invite Users
          </h2>
          <p style={{ margin: '0 0 16px', fontSize: 13, color: colors.textMuted, lineHeight: 1.5 }}>
            Generate a single-use invite link. Anyone with the link can create an account in your organization. Links expire after 7 days.
          </p>

          {inviteError && <div style={errorBanner}>{inviteError}</div>}

          <button style={primaryBtn} onClick={handleCreateInvite} disabled={inviteLoading}>
            {inviteLoading ? 'Generating…' : 'Generate invite link'}
          </button>

          {inviteUrl && (
            <div style={inviteBox}>
              <div style={{ fontSize: 12, color: colors.textMuted, marginBottom: 6 }}>
                Share this link — expires {inviteExpiry}
              </div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <input
                  readOnly
                  value={inviteUrl}
                  style={inviteInput}
                  onFocus={e => e.target.select()}
                />
                <button style={inviteLoading ? primaryBtn : (inviteCopied ? savedBtn : secondaryBtn)} onClick={handleCopyInvite}>
                  {inviteCopied ? 'Copied ✓' : 'Copy'}
                </button>
              </div>
            </div>
          )}
        </section>

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

const backBtn: React.CSSProperties = {
  display:      'inline-block',
  marginBottom: 12,
  padding:      '6px 0',
  background:   'none',
  border:       'none',
  color:        colors.textMuted,
  cursor:       'pointer',
  fontSize:     13,
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

const secondaryBtn: React.CSSProperties = {
  padding:      '9px 16px',
  background:   colors.surface,
  color:        colors.text,
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.md,
  cursor:       'pointer',
  fontSize:     14,
  fontWeight:   500,
  fontFamily:   font.body,
  flexShrink:   0,
}

const savedBtn: React.CSSProperties = {
  ...secondaryBtn,
  background:   colors.successBg,
  color:        colors.successText,
  borderColor:  colors.successBorder,
}

const inviteBox: React.CSSProperties = {
  marginTop:    16,
  padding:      '14px 16px',
  background:   colors.bg,
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.md,
}

const inviteInput: React.CSSProperties = {
  flex:         1,
  padding:      '8px 10px',
  fontSize:     13,
  fontFamily:   font.body,
  background:   colors.surface,
  color:        colors.text,
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.md,
  outline:      'none',
  minWidth:     0,
}
