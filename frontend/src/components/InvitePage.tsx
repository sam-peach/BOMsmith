import { FormEvent, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { acceptInvite, validateInvite } from '../api/client'
import { LogoWordmark } from './Logo'
import { colors, font, radius, shadow } from '../theme'

type State = 'loading' | 'valid' | 'invalid' | 'expired' | 'submitting' | 'done'

export default function InvitePage({ onAccepted }: { onAccepted: () => void }) {
  const { token } = useParams<{ token: string }>()
  const navigate   = useNavigate()
  const [state,    setState]    = useState<State>('loading')
  const [orgName,  setOrgName]  = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm,  setConfirm]  = useState('')
  const [error,    setError]    = useState<string | null>(null)

  useEffect(() => {
    if (!token) { setState('invalid'); return }
    validateInvite(token)
      .then(({ orgName }) => { setOrgName(orgName); setState('valid') })
      .catch(err => {
        const msg = (err as Error).message
        setState(msg.includes('expired') || msg.includes('used') ? 'expired' : 'invalid')
      })
  }, [token])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (password !== confirm) { setError('Passwords do not match.'); return }
    if (password.length < 8)  { setError('Password must be at least 8 characters.'); return }
    setState('submitting')
    try {
      await acceptInvite(token!, username, password)
      setState('done')
      onAccepted()
    } catch (err) {
      setError((err as Error).message)
      setState('valid')
    }
  }

  return (
    <div className="login-bg" style={overlay}>
      <div className="fade-up" style={card}>
        <div style={{ marginBottom: 28 }}>
          <LogoWordmark size={52} />
        </div>

        {state === 'loading' && (
          <p style={muted}>Validating invite link…</p>
        )}

        {(state === 'invalid') && (
          <>
            <p style={errorText}>This invite link is invalid.</p>
            <button style={linkBtn} onClick={() => navigate('/')}>Go to sign in</button>
          </>
        )}

        {state === 'expired' && (
          <>
            <p style={errorText}>This invite link has expired or has already been used.</p>
            <button style={linkBtn} onClick={() => navigate('/')}>Go to sign in</button>
          </>
        )}

        {(state === 'valid' || state === 'submitting') && (
          <>
            <p style={{ margin: '0 0 24px', fontSize: 14, color: colors.textMuted, lineHeight: 1.5 }}>
              You've been invited to join <strong style={{ color: colors.text }}>{orgName}</strong> on BOMsmith.
              Create your account below.
            </p>

            {error && <div style={errorBox}>{error}</div>}

            <form onSubmit={handleSubmit} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <label style={labelStyle}>
                Username
                <input className="field-input" type="text" value={username} autoFocus required
                  autoComplete="username" onChange={e => setUsername(e.target.value)} />
              </label>
              <label style={labelStyle}>
                Password
                <input className="field-input" type="password" value={password} required
                  autoComplete="new-password" onChange={e => setPassword(e.target.value)} />
              </label>
              <label style={labelStyle}>
                Confirm password
                <input className="field-input" type="password" value={confirm} required
                  autoComplete="new-password" onChange={e => setConfirm(e.target.value)} />
              </label>
              <button type="submit" style={submitBtn} disabled={state === 'submitting'}>
                {state === 'submitting' ? 'Creating account…' : 'Create account'}
              </button>
            </form>

            <p style={{ marginTop: 16, fontSize: 12, color: colors.textSubtle, textAlign: 'center' }}>
              Already have an account?{' '}
              <button style={linkBtn} onClick={() => navigate('/')}>Sign in</button>
            </p>
          </>
        )}
      </div>
    </div>
  )
}

const overlay: React.CSSProperties = {
  minHeight: '100vh', display: 'flex', alignItems: 'center',
  justifyContent: 'center', fontFamily: font.body, padding: 24,
}

const card: React.CSSProperties = {
  background: colors.surface, borderRadius: radius.xl,
  padding: '40px 36px', width: '100%', maxWidth: 380,
  boxShadow: shadow.login,
}

const labelStyle: React.CSSProperties = {
  display: 'flex', flexDirection: 'column', gap: 6,
  fontSize: 13, fontWeight: 500, color: colors.text,
}

const submitBtn: React.CSSProperties = {
  marginTop: 8, padding: '11px', background: colors.brand, color: '#fff',
  border: 'none', borderRadius: radius.md, cursor: 'pointer',
  fontSize: 14, fontWeight: 600, letterSpacing: '0.01em', fontFamily: font.body,
}

const errorBox: React.CSSProperties = {
  background: colors.errorBg, color: colors.errorText,
  border: `1px solid ${colors.errorBorder}`, padding: '10px 14px',
  borderRadius: radius.md, fontSize: 14, marginBottom: 16,
}

const errorText: React.CSSProperties = {
  color: colors.errorText, fontSize: 14, margin: '0 0 16px',
}

const muted: React.CSSProperties = {
  color: colors.textMuted, fontSize: 14, margin: 0,
}

const linkBtn: React.CSSProperties = {
  background: 'none', border: 'none', padding: 0, cursor: 'pointer',
  color: colors.brand, fontSize: 12, textDecoration: 'underline',
  fontFamily: font.body,
}
