import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  changePassword, createInvite, getExportConfig, importMappings,
  listMappingClients, saveExportConfig,
} from '../api/client'
import type { ClientMappingSummary, ExportConfig, MappingImportResult } from '../types/api'
import { parseMappingExcel, type ParsedExcel } from '../util/excelMapping'
import { colors, font, radius, shadow } from '../theme'

// ── Column catalogue ─────────────────────────────────────────────────────────

const ALL_COLUMNS: { key: string; label: string }[] = [
  { key: 'internalPartNumber',     label: 'Internal Part Number' },
  { key: 'quantity',               label: 'Quantity' },
  { key: 'unit',                   label: 'Unit' },
  { key: 'description',            label: 'Description' },
  { key: 'lineNumber',             label: 'Line' },
  { key: 'customerPartNumber',     label: 'Customer Part Number' },
  { key: 'manufacturerPartNumber', label: 'Manufacturer Part Number' },
  { key: 'notes',                  label: 'Notes' },
  { key: 'empty',                  label: 'Empty column (spacer)' },
]

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

  const [exportCfg,        setExportCfg]        = useState<ExportConfig>({ columns: ['internalPartNumber', 'empty', 'quantity'], includeHeader: false })
  const [exportSaving,     setExportSaving]     = useState(false)
  const [exportError,      setExportError]      = useState<string | null>(null)
  const [exportSuccess,    setExportSuccess]    = useState(false)

  // Client mappings — Excel import + per-client list.
  const [clients,          setClients]          = useState<ClientMappingSummary[]>([])
  const [pendingClient,    setPendingClient]    = useState<string | null>(null) // which client an in-progress import targets
  const [newClientName,    setNewClientName]    = useState('')
  const [preview,          setPreview]          = useState<ParsedExcel | null>(null)
  const [importing,        setImporting]        = useState(false)
  const [importResult,     setImportResult]     = useState<MappingImportResult | null>(null)
  const [importError,      setImportError]      = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    getExportConfig().then(setExportCfg).catch(() => {})
    listMappingClients().then(setClients).catch(() => {})
  }, [])

  function pickFileFor(clientLabel: string) {
    setImportError(null)
    setImportResult(null)
    setPreview(null)
    setPendingClient(clientLabel)
    fileInputRef.current?.click()
  }

  async function handleFileChosen(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = '' // allow re-pick of same file
    if (!file) return
    try {
      const parsed = await parseMappingExcel(file)
      setPreview(parsed)
      if (parsed.rows.length === 0) {
        setImportError('No rows with a recognisable customer part number column found in this file.')
      }
    } catch (err) {
      setImportError((err as Error).message)
    }
  }

  async function confirmImport() {
    if (pendingClient === null || !preview) return
    setImporting(true)
    setImportError(null)
    try {
      const result = await importMappings(pendingClient, preview.rows)
      setImportResult(result)
      setPreview(null)
      // Refresh the clients list so counts reflect the import.
      listMappingClients().then(setClients).catch(() => {})
    } catch (err) {
      setImportError((err as Error).message)
    } finally {
      setImporting(false)
    }
  }

  function cancelImport() {
    setPreview(null)
    setImportError(null)
    setPendingClient(null)
  }

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

  function toggleExportColumn(key: string, checked: boolean) {
    setExportCfg(prev => ({
      ...prev,
      columns: checked
        ? [...prev.columns, key]
        : prev.columns.filter(c => c !== key),
    }))
  }

  function moveColumn(key: string, dir: -1 | 1) {
    setExportCfg(prev => {
      const cols = [...prev.columns]
      const idx = cols.indexOf(key)
      if (idx < 0) return prev
      const next = idx + dir
      if (next < 0 || next >= cols.length) return prev
      ;[cols[idx], cols[next]] = [cols[next], cols[idx]]
      return { ...prev, columns: cols }
    })
  }

  async function handleSaveExportConfig(e: React.FormEvent) {
    e.preventDefault()
    setExportError(null)
    setExportSuccess(false)
    if (exportCfg.columns.length === 0) {
      setExportError('At least one column must be selected.')
      return
    }
    setExportSaving(true)
    try {
      const saved = await saveExportConfig(exportCfg)
      setExportCfg(saved)
      setExportSuccess(true)
    } catch (e) {
      setExportError((e as Error).message)
    } finally {
      setExportSaving(false)
    }
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

        <section style={{ ...card, marginBottom: 16 }}>
          <h2 style={{ margin: '0 0 8px', fontSize: 15, fontWeight: 600, color: colors.text }}>
            Client Mappings
          </h2>
          <p style={{ margin: '0 0 16px', fontSize: 13, color: colors.textMuted, lineHeight: 1.5 }}>
            Import a client's part-number Excel file so future drawings from that
            client land with Confirmed cells on every known part. Each client's
            mappings live in their own bucket so two clients can use the same
            customer P/N without colliding.
          </p>

          <input
            ref={fileInputRef}
            type="file"
            accept=".xlsx,.xls"
            style={{ display: 'none' }}
            onChange={handleFileChosen}
          />

          {importError && <div style={errorBanner}>{importError}</div>}
          {importResult && (
            <div style={successBanner}>
              Imported into <strong>{pendingClient || 'generic'}</strong>: {importResult.saved} new,
              {' '}{importResult.overwritten} overwritten{importResult.skipped ? `, ${importResult.skipped} skipped` : ''}.
            </div>
          )}

          {preview && pendingClient !== null && (
            <div style={{ ...inviteBox, marginBottom: 16 }}>
              <div style={{ fontSize: 13, marginBottom: 8 }}>
                Ready to import {preview.rows.length} rows into
                {' '}<strong>{pendingClient || '(generic bucket)'}</strong>.
              </div>
              <div style={{ fontSize: 12, color: colors.textMuted, marginBottom: 4 }}>
                Recognised columns: {preview.recognisedColumns.join(', ') || '(none)'}
              </div>
              {preview.ignoredColumns.length > 0 && (
                <div style={{ fontSize: 12, color: '#92400e', marginBottom: 8 }}>
                  Ignored columns: {preview.ignoredColumns.join(', ')}
                </div>
              )}
              <div style={{ display: 'flex', gap: 8 }}>
                <button
                  style={primaryBtn}
                  onClick={confirmImport}
                  disabled={importing || preview.rows.length === 0}
                >
                  {importing ? 'Importing…' : 'Confirm import'}
                </button>
                <button style={secondaryBtn} onClick={cancelImport} disabled={importing}>
                  Cancel
                </button>
              </div>
            </div>
          )}

          <div style={{ border: `1px solid ${colors.border}`, borderRadius: radius.md, overflow: 'hidden', marginBottom: 12 }}>
            {clients.length === 0 && (
              <div style={{ padding: '12px 14px', fontSize: 13, color: colors.textMuted }}>
                No mappings yet — use "Import for new client" below to bring in your first client's part-number list.
              </div>
            )}
            {clients.map(c => (
              <div key={c.label || '_generic_'} style={{
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                padding: '10px 14px', borderBottom: `1px solid ${colors.borderLight}`,
              }}>
                <span style={{ fontSize: 13 }}>
                  {c.label
                    ? <strong>{c.label}</strong>
                    : <span style={{ color: colors.textMuted }}>(generic / untagged)</span>}
                  <span style={{ marginLeft: 8, color: colors.textMuted, fontSize: 12 }}>
                    {c.count} {c.count === 1 ? 'mapping' : 'mappings'}
                  </span>
                </span>
                <button style={secondaryBtn} onClick={() => pickFileFor(c.label)}>
                  Import Excel
                </button>
              </div>
            ))}
          </div>

          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <input
              value={newClientName}
              onChange={e => setNewClientName(e.target.value)}
              placeholder="New client name"
              style={{
                flex: 1, maxWidth: 240, padding: '6px 10px',
                border: `1px solid ${colors.border}`, borderRadius: radius.sm, fontSize: 13,
              }}
            />
            <button
              style={primaryBtn}
              disabled={newClientName.trim() === ''}
              onClick={() => {
                const name = newClientName.trim()
                setNewClientName('')
                pickFileFor(name)
              }}
            >
              Import for new client
            </button>
          </div>
        </section>

        <section style={{ ...card, marginBottom: 16 }}>
          <h2 style={{ margin: '0 0 8px', fontSize: 15, fontWeight: 600, color: colors.text }}>
            SAP Export
          </h2>
          <p style={{ margin: '0 0 16px', fontSize: 13, color: colors.textMuted, lineHeight: 1.5 }}>
            Choose which columns appear in the SAP export and their order.
            Rows without an Internal Part Number are always omitted.
          </p>

          {exportError  && <div style={errorBanner}>{exportError}</div>}
          {exportSuccess && <div style={successBanner}>Export settings saved.</div>}

          <form onSubmit={handleSaveExportConfig}>
            <div style={{ border: `1px solid ${colors.border}`, borderRadius: radius.md, marginBottom: 20, overflow: 'hidden' }}>
              {ALL_COLUMNS.map(({ key, label }) => {
                const included = exportCfg.columns.includes(key)
                const idx      = exportCfg.columns.indexOf(key)
                return (
                  <div key={key} style={{ ...columnRow, padding: '8px 12px' }}>
                    <label style={{ display: 'flex', alignItems: 'center', gap: 8, flex: 1, cursor: 'pointer' }}>
                      <input
                        type="checkbox"
                        checked={included}
                        onChange={e => toggleExportColumn(key, e.target.checked)}
                      />
                      <span style={{ fontSize: 13 }}>{label}</span>
                    </label>
                    {included && (
                      <div style={{ display: 'flex', gap: 2 }}>
                        <button
                          type="button"
                          style={arrowBtn}
                          disabled={idx === 0}
                          onClick={() => moveColumn(key, -1)}
                          aria-label="Move up"
                        >↑</button>
                        <button
                          type="button"
                          style={arrowBtn}
                          disabled={idx === exportCfg.columns.length - 1}
                          onClick={() => moveColumn(key, 1)}
                          aria-label="Move down"
                        >↓</button>
                        <span style={{ fontSize: 11, color: colors.textMuted, minWidth: 20, textAlign: 'right', lineHeight: '28px' }}>
                          {idx + 1}
                        </span>
                      </div>
                    )}
                  </div>
                )
              })}
              <div style={{ padding: '8px 12px', borderTop: `1px solid ${colors.border}`, background: colors.bg }}>
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
                  <input
                    type="checkbox"
                    checked={exportCfg.includeHeader}
                    onChange={e => setExportCfg(prev => ({ ...prev, includeHeader: e.target.checked }))}
                  />
                  <span style={{ fontSize: 13 }}>Include header row</span>
                </label>
              </div>
            </div>

            <button type="submit" style={primaryBtn} disabled={exportSaving}>
              {exportSaving ? 'Saving…' : 'Save export settings'}
            </button>
          </form>
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
  maxWidth: 640,
  margin:   '0 auto',
  padding:  '36px 24px 72px',
}

const card: React.CSSProperties = {
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

const columnRow: React.CSSProperties = {
  display:        'flex',
  alignItems:     'center',
  justifyContent: 'space-between',
  padding:        '8px 0',
  borderBottom:   `1px solid ${colors.border}`,
}

const arrowBtn: React.CSSProperties = {
  padding:      '0 7px',
  height:       28,
  background:   colors.bg,
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.sm,
  cursor:       'pointer',
  fontSize:     13,
  color:        colors.text,
  fontFamily:   font.body,
  lineHeight:   '26px',
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
