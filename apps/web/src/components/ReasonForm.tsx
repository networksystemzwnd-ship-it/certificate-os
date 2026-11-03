import { useState, type FormEvent } from 'react';

/** Inline confirmation for a consequential action: says what will happen, asks for a reason, and can be cancelled. */
export function ReasonForm({ title, consequence, confirmLabel, onConfirm, onCancel, busy }: {
  title: string; consequence: string; confirmLabel: string; busy?: boolean;
  onConfirm: (reason: string) => void; onCancel: () => void;
}) {
  const [reason, setReason] = useState('');
  const trimmed = reason.trim();
  const valid = trimmed.length >= 3 && trimmed.length <= 500;
  const submit = (e: FormEvent) => { e.preventDefault(); if (valid) onConfirm(trimmed); };
  return (
    <form className="reason" role="group" aria-label={title} onSubmit={submit}>
      <strong>{title}</strong>
      <p className="muted">{consequence}</p>
      <label htmlFor="reason-input">Reason (3–500 characters, shown to your team)</label>
      <input id="reason-input" value={reason} maxLength={500} autoFocus onChange={(e) => setReason(e.target.value)} />
      <div className="row-actions">
        <button type="submit" className="danger" disabled={!valid || busy}>{confirmLabel}</button>
        <button type="button" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  );
}
