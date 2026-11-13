import { useState } from 'react'
import { KeyRound, X } from 'lucide-react'
import { ApiError, changeOwnPassword } from '../../shared/api'

export function ChangePasswordDialog({ onClose, onChanged }: { onClose: () => void; onChanged: () => void }) {
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = (event: React.FormEvent) => {
    event.preventDefault(); setError('')
    if (newPassword !== confirmation) { setError('两次输入的新密码不一致'); return }
    setBusy(true)
    changeOwnPassword(currentPassword, newPassword).then(onChanged)
      .catch((e: ApiError) => setError(e.message || '修改密码失败')).finally(() => setBusy(false))
  }
  return <div className="modal-layer" role="presentation"><button className="modal-scrim" aria-label="关闭修改密码" onClick={onClose} /><form className="secret-dialog change-password-dialog form-grid" role="dialog" aria-modal="true" aria-labelledby="change-password-title" onSubmit={submit}><div className="dialog-heading"><div><span className="eyebrow">账号安全</span><h2 id="change-password-title">修改密码</h2></div><button type="button" className="icon-button" aria-label="关闭" onClick={onClose}><X size={18} /></button></div>{error && <div className="login-error">{error}</div>}<label><span>当前密码</span><input type="password" autoComplete="current-password" minLength={8} maxLength={72} required value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} /></label><label><span>新密码</span><input type="password" autoComplete="new-password" minLength={8} maxLength={72} required value={newPassword} onChange={(event) => setNewPassword(event.target.value)} /></label><label><span>确认新密码</span><input type="password" autoComplete="new-password" minLength={8} maxLength={72} required value={confirmation} onChange={(event) => setConfirmation(event.target.value)} /></label><div className="form-actions"><button type="button" className="quiet-button" onClick={onClose}>取消</button><button className="primary-button" disabled={busy || newPassword.length < 8 || newPassword !== confirmation}><KeyRound size={15} />确认修改</button></div></form></div>
}
